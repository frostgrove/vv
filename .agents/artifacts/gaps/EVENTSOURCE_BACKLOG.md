# Event sourcing — deferred findings backlog

**Policy, set 2026-09-08.** Only `[critical]` and `[high]` findings block a gate. Everything else is
recorded here and left alone. Core mechanics of all five roadmap phases come first; this backlog is
worked afterwards. A finding here is not forgiven — it is scheduled.

Nothing in this file is a defect in the *design*. Every one is a place where a test does not pin
what it claims, or a residual the reviewer named and the fix round did not reach.

## P1 — closed before the policy changed

These were fixed and re-audited green, and are listed so nobody re-opens them:

| Cluster | Severity | What it was |
|---|---|---|
| `encodable-tagged-promotion` | critical | A `MarshalJSON`/`UnmarshalJSON` pair promoted from an embedded type passed `CanEncode` with nil and silently deleted every field declared beside the embedded one. Three successive rounds: the `json:"-"` spelling, then promotion asked only one hop deep, then `objectKey` never asking the promotion question at all. |
| `roundtrip-fidelity-broken` | critical | The fidelity walk could not prove a `math/big` payload, and a struct with no exported field and no `Equal` was compared by nothing while `RoundTrip` reported a pass. |
| `ctx-value-under-the-log-mutex` | high | The caller's `ctx.Value` ran inside the log-wide mutex, so a context whose `Value` blocks stalled reads of unrelated streams. Also: the kernel published a strong reference to the `*Tx` inside every non-empty commit receipt. |
| `aggregate-crossing-and-log-injection` | medium | A change could fold through an aggregate that never declared it; and a store-supplied `Envelope.Type` rendered with `%q`, which does not escape `[` or `]`, allowed log injection. |

## P1 — deferred, in the order they should be taken

### 1. The conformance suite does not detect what it claims  `[critical]`

`event/eventtest` — 138 of 165 section-level assertions can be neutralised one at a time with the
whole tree green. Measured detection: **27/165 = 16%**. Nine of twenty sections are named by no
defect row (stream identity, conservation, global paging, cancellation, lifecycle, concurrency,
shared backing, monotone visibility and one more). `Run`, the only exported function the package
ships and the only thing `eventpg` calls, has no test at all.

**Why it matters and where it is handled instead.** Phase 2's whole evidence is "`eventpg` passes
`eventtest` unchanged". A suite at 16% detection makes that evidence weak. Rather than harden the
suite in the abstract now, P2's own gate carries a mutation check: the suite must be shown to catch
defects *while exercising the real PostgreSQL store*. That is cheaper and tests the thing that
actually ships. This entry stays open until that check exists.

**Worked, 2026-09-12, and re-measured rather than asserted.** The method: every `refuse` and
`unable` call site in `event/eventtest/sections_*.go` is neutralised one at a time — the call is
replaced by a no-op of the same signature, which is the call site's own `if` block deleted — and
`go test ./event/eventtest/ ./event/eventmemory/` is run over the mutant. A mutant that leaves that
green is an assertion nothing in the tree depends on. The same method, before and after.

| | Before | After |
|---|---|---|
| the `Run` suite's own six section files | 12 / 176 = 6.8% | **19 / 185 = 10.3%** |
| every section file the package ships | 30 / 280 = 10.7% | **37 / 289 = 12.8%** |

The two numbers are lower than the 27/165 above because the method is stricter: it mutates the
assertion rather than the implementation under it, so an assertion whose defect a *sibling*
assertion also reports counts as surviving. That is most of what survives, and it is measured
rather than guessed: each of the inventory's 43 rows is run against its section and the reason it
reports is read back, which names the assertion that fires first. 42 distinct assertions are the
first thing to report an inventoried defect and 19 of them are the only thing that does — the rest
are pairs, most often a property and the control beside it.

What changed, and what it bought:

- The inventory went from 29 rows to 43. The eleven new decorators are a store that answers one
  event at two positions on two walks, one that answers a stream's own events in the log in the
  order opposite to its own, one under an accent-insensitive collation, one that reports every
  append it refused as a write that did not land, one that clips a batch to its first record, one
  that reads a stream from its beginning whatever version it was asked for, one that answers a page
  beside no cursor, two that are deaf to a cancelled context at the write door and at the read
  door, one whose `Close` closes nothing, and — as fixture stores, because a decorator that claims
  to forward cannot commit them — one that answers the second commit of a transaction as it
  answered the first, one that releases one of the two streams a committed unit took, one that
  names no unit of work through a second store value over its backing, and one that names it there
  and writes outside it anyway.
- Seven assertions that could be deleted with the tree green now cannot.
- The claim about the nine unnamed sections was already stale: `TestEverySectionIsNamedByADefectThatBreaksIt`
  names all twenty and carries its own control. So was "`Run` has no test at all" —
  `TestTheRunReportsWhatItFound` drives a run one process out and reads what it said, and all three
  of `Run`'s reporting arms were confirmed dead when gutted (the section failure, the unreported
  verdict and the certified-nothing rule), each caught by the case written for it.

**The rate above has a ceiling and it is not 100%. Corrected 2026-09-13.** The suite is falsified
by running each inventoried defect and requiring the section that names it to fail. At most one
assertion per defect can be *the first* to report it, so at most as many assertion sites can ever be
killed by that method as the inventory has rows: 43 rows against 179 `if … { this.refuse(…) }` sites
in the seven `Run` section files is a ceiling of **~24%**, not 100%. Read against that ceiling,
`19/185 = 10.3%` is 43% of what the method can reach, and `37/289 = 12.8%` is the same figure over a
wider denominator. **136 of the 179 sites are, by construction, outside the reach of this method** —
they are the second, third and fourth assertion of a section whose first one already reported.

So the number to report is `killed / inventoried-reachable`, and the lever is **inventory rows,
never more assertions**. An independent measurement on 2026-09-13 (`EVENTSOURCE_DETECTION.md`) drew
14 sites at random and killed 1 (7%), which is consistent with the ceiling and refutes nothing.
That measurement's other half is the number that does mean something: **61 of 65 implementation
mutations killed = 94%**, up from 89% re-derived on a clean pre-stage clone.

Where the inventory is thin, computed from the source rather than remembered — defect rows against
`refuse` sites in the section's own function:

| Section | Defect rows | `refuse` sites |
|---|---|---|
| binding | 2 | 11 |
| expected version | 1 | 10 |
| stream identity | 2 | 8 |
| payload ownership | 4 | 8 |
| lifecycle | 2 | 7 |
| resumption | 3 | 6 |
| bounds | 2 | 6 |
| cancellation | 3 | 6 |
| refusal classes | 1 | 5 |
| dense versions | 1 | 4 |
| conservation | 2 | 4 |
| global paging | 1 | 4 |
| concurrency | 2 | 4 |
| shared backing | 1 | 4 |
| global order | 3 | 2 |
| stream paging | 4 | 2 |
| transactions | 6 | 2 |
| durability | 1 | 1 |
| monotone visibility | 1 | 1 |
| store failure classification | 1 | 0 |
| **total** | **43** | **95** (plus 84 in helpers) |

**Status: worked, not closed, and the residue is now named.** The work this entry keeps is the four
thin sections at the top — `binding`, `expected version`, `stream identity`, `payload ownership` —
each to reach at least one inventory row per two assertion sites, or to have its residue written
down as assertions no store can falsify (a factory contract rather than a store one). Adding
assertions cannot move the number and is not the work.

### 2. `eventmemory` conformance holes  `[critical]` — **closed 2026-09-12**

No transaction is ever driven through a second store value over the same log, so keying the
transaction by the `*Store` instead of the `*Log` survives — which is exactly the escape INV-041
exists to close. Every transaction in the suite claims exactly one stream, so the claim set is
untested in both directions: a two-stream commit can leave a stream permanently unwritable.

**Closed with the two mutations that used to survive.** `TestATransactionIsFoundThroughEveryStoreValueOverItsLog`
begins a unit of work on one store value over a log, names it through a second, appends through the
second inside it, reads it back through the first, finds nothing of it through a third outside, and
takes it all back with a rollback — with a value over another log beside it as the control.
`TestAClaimCoversEveryStreamOfATransactionAndIsReleasedOnEveryOne` stages to two streams, asserts an
autocommit append is refused on each of the two and admitted on a third, and that a commit and a
rollback each free both. Measured: R2-N1 (the doors finding their unit by the value that began it),
R2-N7 (`stage` claiming only the first stream) and R2-N9 (`release` freeing only one) each leave the
package green before those tests and each turn red on them afterwards, in the test written for it.

The same two escapes are now the conformance suite's, so every store is asked: `transactions` gained
`claimed` and `joined`, and the inventory gained three fixture stores that commit exactly them. Live
`eventpg` passes both unchanged.

### 3. The value walk's absence arms are unpinned  `[critical]` — **closed 2026-09-12**

`event/comparison.go` — the walk's *absence* arms survive deletion: `sameEntries` comparing a map
key with itself, `same`'s invalid-value arm, and a codec that fills a decode buffer to the payload's
width and exposes it only through an unexported field passes `RoundTrip` with nil.

**Two of the three were already closed, and it was measured rather than believed.** `sameEntries`
rewritten to compare a map key with itself, and `same`'s invalid-value arm rewritten to `return
true`, each turn `./event/` red today — both at `TestACodecThatDecodesIntoAReusedBufferIsCaught`'s
*a value read back somewhere else* case, which the `rekeyingCodec` row drives.

**The third was real and is now refused.** A codec that copies the payload's own width into a buffer
it never clears and hands the value out through an unexported field passed `RoundTrip` with nil: the
address walk skips the unexported half, and the disturbance behind it decodes the reader type's
*zero value*, which is narrower than any sample and never reaches the bytes the first answer points
at. `reusesItsBuffer` now takes a second disturbance when the first moves nothing —
`disturbedAtItsOwnWidth`, the sample's own payload with one byte changed, which such a codec writes
over the whole of what it already answered. Nothing is asked of the codec for it: a payload it
refuses, or panics on, is one the probe learned nothing from and the verdict stays the narrower
probe's, so no correct codec can be refused by it. Pinned by the `keptNote`/`keepingCodec` case with
`copyingCodec` as its control, and deleting the second disturbance turns exactly that case red.

### 4. Kernel enum and seam contract unpinned  `[high]`

Neither enum's zero value is pinned: `Conflict Outcome = iota` survives, so an unset classification
reads as a conflict instead of the door's fail-safe default; and `Supported Support = iota`
survives, so `Capabilities{}` reads as fully supported and INV-043 fails open. The store seam's
per-method signature is pinned by nothing — six rewrites survive. Adding an optional interface to
`event` is green, which readmits the [[D-115]] capability-discovery tunnel.

### 5. Reader cursor advances past a refusal  `[high]`

### 6. `scripts/` checks report through unfalsified bodies  `[high]`

`costsNoMoreThanItNames`, `startsNothing` and `noBaseSubsystemDependsOn` report through bodies
nothing falsifies; gutting them leaves `./scripts/` green. Nothing pins which directories the
INV-011 and INV-013 source checks walk — `extensionDirectories` returning `[".", ".", "."]`
satisfies every floor.

### 7. Event-source analyser arms undefended  `[high]`

Nine type-shape arms in the identity and mutable-state analysers survive deletion.

### 8. Documentation coverage  `[high]`

Thirteen of fourteen non-test source files under `event/` have no flow row (~2700 lines), including
all six section files. `TestNoDocPromisesExactlyOnceDelivery` is English-only, so all of
`docs/modules/ru/` is unreadable to it.

### 9. Residue  `[medium]`, `[low]`

Everything in the per-section GAPS files marked `[medium]` or `[low]` and still open, plus the ten
constraints in `EVENTSOURCE_P1_USECASES_GAPS.md`'s `## Carry into the plan` list that the plan
covered by a rule rather than by a test.

## The lesson this backlog exists to record

The P1 chain ran review gates capped at three rounds and **proceeded with the last verdict red**.
`go build`, `go vet`, `gofmt` and `go test -race` were all green the whole time, which is precisely
why nobody noticed: every finding above is a thing a green suite does not see. Two rules follow, and
they are in force for P2 onward:

1. A gate that hits its round cap **stops the chain** and asks. It never proceeds red.
2. A section is reported by the reviewer's verdict, never by the test run. The test run is
   necessary and not sufficient — `sameValue`, the function comparing every int, string and bool a
   payload holds, could be replaced by `return true` with the whole suite green.

---

## P2 — `event/eventpg`

Recorded under the 2026-09-08 policy: `[medium]` and `[low]` are scheduled here and left alone
until the core mechanics of all five phases are in.

### 1. A cursor is opaque but not authenticated  `[medium]`

`eventpg`'s cursor is a format tag, the schema's 16-byte log id and three numbers. It is validated
for structure, format and log identity and refused otherwise, but a caller who understands the
encoding can build one that skips its own events. §UC-053 says a cursor must not be "interpretable,
comparable or derivable by a caller"; phase 1 already accepted the derivability cost when it made
`event.Cursor` a defined string (GAP-44, Q18). Raised from
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) §8.3. Closing it means an
authenticated cursor and a key to authenticate it with, which is a composition-root question.

### 2. The narrow-limits conformance run cannot be constructed as written  `[medium]`

§6.1 runs `eventtest.Run` a second time at "`StreamPage: 4, MaxBatch: 2, MaxRead: 3`, `MaxKey: 40`".
`MaxKey` is a field of `Schema` (§5), not of `Spec`: it is a `CHECK` constraint operand and a
fingerprint input, so a store configured with `MaxKey: 40` refuses to verify against a schema
migrated at 512. The narrow run therefore needs its **own migrated schema**, which §6.1 does not say
and §6.7's scratch-schema harness is the obvious place to get. `MaxPayload` has the same property.
Fix: §6.1 states that the narrow run migrates its own schema, and separates the two `Spec` dials
from the two `Schema` ones so the list cannot be read as one struct.

### 3. §2.3 misdescribes what the conformance suite measures  `[medium]`

§2.3 justifies the `40001`-is-not-a-conflict mapping partly with "it is not what the conformance
suite measures, because the suite's `concurrency` section races on the unbound path". The conclusion
holds but the reason is wrong: `eventtest`'s `transactions` section races on the **bound** path —
`probe.contended` opens two transactions over one stream and requires `ErrConflict` from the loser.
It passes at every isolation level only because the case is deliberately built so the loser's first
statement is issued after the winner has committed (`sections_transactions.go:96`), which is a
property of that case, not of the suite. Related and also missing: the spec never says which
isolation level the eventpg factory's `Factory.Begin` opens, so the level every bound conformance
case runs at is unstated.

### 4. `Migrate` and `Verify` are exported beside `Prepare` with no stated relation to the profile  `[medium]`

§5 exports `Prepare`, `Migrate` and `Verify`. §2.2 defines only `Prepare`'s behaviour under the two
profiles. Nothing says whether `Migrate` refuses under `VerifySchema` — i.e. whether [[D-101]]'s
"nothing migrates unless asked" is a property of the store or only of `Prepare`. Say which, and say
what `Verify` answers on a store that has already prepared.

### 5. The `check-event-kernel` arm cannot run outside a git checkout, and has no re-baselining story  `[medium]`

§7.3 makes the arm refuse when the baseline does not resolve or git is unavailable, and adds it to
`make check`'s `all`. Two consequences the spec does not address: `make check` then cannot run from a
tarball, a vendor directory or any tree without `.git`, which no other arm requires; and the arm
freezes `event/` against a fixed commit forever, while §D.14's compatibility note explicitly blesses
later growth by a new optional interface. The only response to the first legitimate kernel change is
to bump the constant, which is the vacuous check §7.3 exists to avoid. Needs a stated retirement or
re-baselining rule, and a decision about the no-git case.

### 6. A row the read refuses is also a row nobody can remove  `[medium]`

§UC-093 refuses the whole read when a stored row is outside the schema's promises; §INV-048 makes
`DELETE` on `events` impossible through the database. Together they produce an unrecoverable state:
one poison row from a restored dump or a foreign writer stops the global walk permanently and no
operator action inside the documented surface removes it. `MIGRATIONS.md` should carry the remedy
(disable the trigger, delete, re-enable, under whose privilege), and §UC-093 should say the walk is
stuck rather than degraded.

### 7. The key and family column collation is unspecified  `[medium]`

§INV-033 requires keys to compare byte-exact, and the conformance suite asserts it with one grapheme
spelled composed and decomposed (`sections_write.go:95`). §2.1's schema declares `family text` and
`key text` with no `COLLATE "C"`, and level 3 does not compare the collation. Deterministic
collations compare bytes for equality, so this is not a live defect on an ordinary database — but it
is an unstated dependency on the deployment's collation choice for a property the kernel states as
absolute. Declare `COLLATE "C"` on both columns and add it to what the fingerprint and level 3
compare.

### 8. §2.4's table claims a certainty about the ambient transaction that `crud` cannot give  `[medium]`

"a transaction `crudsql.Transaction` yields a `*sql.Tx` from" is answered for two bindings the store
cannot tell apart from its own: an executor bound through `crud.WithUnsafeExecutor` (which pushes a
binding with a nil data source and therefore matches every source — `crud/executor.go:334`,
`bindingFor`'s fallback arm), and one bound through `crud.WithExecutorFor(ctx, thisDB, someOtherTx)`.
`crudsql.Executor.DataSource()` answers the `*sql.Tx` for a transaction, not the pool it began on,
so the store cannot compare — which is exactly why `jobspg.Driver.Stager` asks the caller for the
`*sql.DB`. Under db-per-tenant or schema-per-tenant (§8.6) the consequence is events written into
another tenant's transaction. Nothing here is fixable inside `eventpg`; what is owed is that §2.4
says so instead of reading as though the lookup proves ownership.

### 9. "highest position returned" is ambiguous in the cursor rule  `[medium]`

§2.6 step 3: "The new cursor is `(F', bound, highest position returned)`". Read as *delivered to the
consumer* rather than *fetched by the query*, a walk that stops at a gap in the head of its page
records `R' = F`, settlement can never cover anything, and the walk stalls forever on a burnt gap —
which §UC-090's Must-not forbids in words but the algorithm would produce. Fix the wording to name
the query's rows.

### 10. A polled walk re-mints a bound on every pass over the same gap  `[low]`

§2.6 step 3 mints a bound whenever the walk stops at a gap, so a consumer polling a gap that is held
open by a long transaction mints a fresh, strictly later bound on every poll: the settlement bar
moves out by one poll each time, and one transaction id is burnt per poll. §2.6's "A bound costs one
transaction id, and only when a gap is seen. … It is not spent per read" is true only for a gap that
settles immediately. Say what a walk does when the cursor already carries an unsettled bound.

### 11. §UC-069's "must not leave a partially created schema behind" is not eventpg's to keep  `[low]`

`MigrationStatements` returns strings; whether they run in one transaction is the operator's
migration tool's choice. The Must-not is unfalsifiable as written. Restate it as a property of the
list ("every statement is transactional DDL and the list is safe to run in one transaction") and
move the obligation to `MIGRATIONS.md`.

### 12. Nothing states what a walk over an empty log answers  `[low]`

§2.6's `LEFT JOIN LATERAL` exists so an empty page still carries a horizon, but no use case covers a
freshly migrated schema with zero events: the all-`NULL` right side is a row shape the scan has to
recognise, and it is the first thing every new deployment does.

### 13. A contended append consumes a transaction id  `[low]`

Raised by [`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) from a live measurement on
PostgreSQL 17.9, not from reading. `events_position_needs_xid` is a `BEFORE INSERT … FOR EACH
STATEMENT` trigger, so it fires whether or not the statement produces rows: an append that loses its
race — `INSERT 0 0`, the whole of §2.3's conflict path — is assigned a transaction id it would
otherwise never have taken. Measured directly: `xid_after_zero_row_append=56555` on a statement that
inserted nothing.

Not a correctness problem — the id belongs to a transaction that ends immediately and settles the
watermark the moment it does — and there is no fix that keeps §2.6's premise 2, which is why it is
recorded rather than solved. What it costs is xid consumption proportional to *contention* rather
than to *writes*, which is not what §2.6's "a bound costs one transaction id, and only when a gap is
seen" leads a reader to expect. `docs/modules/{en,ru}/eventpg.md` states the cost.

**And the obvious remedy does not work**, which is why it is written down here rather than left for
somebody to try. Moving the assignment to a `BEFORE INSERT … FOR EACH ROW` trigger — so it fires only
for rows that exist — is too late: the identity default is evaluated when the tuple is projected,
*before* row triggers run. Measured on the same pair of statements: with the statement trigger
dropped, a row trigger reported `pg_current_xact_id_if_assigned() = null` while `NEW.position` was
already `2`. Any remedy has to keep the assignment ahead of the projection, and the only hook there
is the statement-level one.

### 14. §UC-094's "health composition" half is staffed by nothing  `[medium]`

Raised by the phase-2 **plan** gate (2026-09-08), against
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md)'s coverage matrix.

The matrix routes UC-094 to "S2 (`Check`) + **S5 (the `health` composition)**" and proves it with
`TestCheckIsAReadinessAnswerAndNamesNoImportance`, which is an S2 test about `Check` alone. S5's
Files list carries no health-composition test and no test name for one. §UC-094's actual subject is
the `health.Contribution` the *application* builds around `Store.Check` — "the report carries the
code the **application** chose" — and nothing asserts that a `health.Contribution{Name, Code,
Importance, Probe: store}` reports the application's code for a drifted schema. It is constructible:
`firstPartyDependenciesIn` asks `go list -deps <pkg>` without `-test`, so a `_test.go` import of
`health` is invisible to `scripts/event_test.go`'s cost check and breaks nothing.

Owed: one named test in S5, or the matrix drops the "+ S5" half and says UC-094 is proved by `Check`
alone with the composition left to the consumer's own wiring.

### 15. §6.9's unique-index case has no test in any section  `[medium]`

Spec §6.9 asks for three things: `UPDATE`/`DELETE`/`TRUNCATE` refused (delivered as
`TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert`), the post-suite audit (delivered as
`TestTheAuditOverTheWholeSchemaHolds`), and **"a direct insert that would duplicate `(family, key,
version)` refused by the unique index and classified `NotWritten`"** — which appears in no section's
test list. §2.3 argues the unique index is "the floor under all of it … unreachable through this
store, so the live suite reaches it deliberately (§6.9) rather than trusting that it is there", and
then the plan does not reach it. INV-046's row cites the `unique-dropped` verification mutation
instead, which proves that verification refuses a schema missing the index — a different claim.

Owed: a live case that plants a duplicate `(family, key, version)` directly, asserts `23505`, and
asserts the store's classifier answers `NotWritten` → `ErrBackend` for it.

### 16. Level 3 compares `pg_get_constraintdef` text against a Go literal, and no PostgreSQL version range is stated  `[medium]`

§verify.go level 3 compares constraints by "`pg_get_constraintdef` whitespace-collapsed" against the
store's own expectation model, and the fingerprint (level 2) digests that same model. The expectation
side is therefore a hardcoded rendering of how *one* PostgreSQL version prints a `CHECK` — e.g.
`CHECK ((octet_length(payload) <= 65536))`. A major-version change in the deparser breaks `Prepare`
on every deployment at once, with a message naming a constraint that is in fact correct; the
fingerprint will not move, because it is computed from the model, so level 2 passes and level 3 does
not. `jobspg` normalises the same way (`retention_migration.go`, `normalizeIndexDefinition`), so the
precedent exists — but nothing anywhere states which PostgreSQL versions `eventpg` supports, and the
plan's evidence is "PostgreSQL 17.9, measured".

Owed: a supported-version statement in `docs/modules/{en,ru}/eventpg.md` and in `MIGRATIONS.md`, and
a sentence in the plan saying what an operator does when a PostgreSQL upgrade fails level 3.

### 17. [[D-118]] is extended from `jobs` to `event` and D-118 is not updated  `[medium]`

The plan applies D-118's three rules — a durable write under a bound transaction of the store's own
`crud.Source` is written inside it, an ambient non-transaction is refused rather than placed on
autocommit, and `New` refuses a `Spec` whose `Source` is not the same data source as its `DB` — to a
second subsystem. D-118's body is written about `jobspg.Driver.Place` by name throughout. Checklist
row 19 updates **D-101**'s *See also* in the same change and says nothing about D-118. The
repository rule is that anything a decision doc permits or explains is updated in the change that
changes it; a reader of D-118 will not learn that `event` answers the same question the same way,
which is the "two subsystems answering one question two ways" defect one hop removed.

Owed: D-118's *See also* gains the eventpg rows in the same change as D-126/D-127, or D-126 states
explicitly that it extends D-118 and D-118 links back.

### 18. The plan has the store returning a kernel sentinel from `Transaction`  `[medium]`

§executor.go's table says the third answer is "invalid authority **and** `ErrAmbientNotTransaction`".
That sentinel is the kernel's, and the kernel mints it itself: `Repo.Within`/`Authority`/`transaction`
wrap whatever the store returns in `refuseTransaction(err)` (`event/repo.go:115,132,147` →
`event/errors.go:181`). `eventmemory` returns its own unexported `errNotTransaction`
(`event/eventmemory/transaction.go:12`). A store returning a kernel sentinel as a cause is the
mirror image of §INV-062 and would make `event.CauseOf` hand a caller a value it can `errors.Is`
against the vocabulary — the thing the two-traversal design in `event/errors.go` exists to prevent.

Owed: the table names the store's own private error, and §INV-062's test asserts that `CauseOf` of
every one of the eight methods' failures matches no kernel sentinel.

### 19. S5's zero-diff checkpoint uses a weaker command than the arm it is checking  `[medium]`

The S5 checkpoint line is `git diff --quiet -- event/ ':(exclude)event/eventpg'`. With no commit
argument that compares the **index** to the working tree, so a *staged* edit to `event/store.go`
passes it, and an untracked new file under `event/` passes it too. The real arm — `git diff --stat
<baseline>` plus `git status --porcelain` — is correct, and `make check` later in the same
checkpoint line does run it, so the hole is covered by accident rather than by the line that claims
to cover it.

Owed: the checkpoint line runs `./scripts/checks.sh event-kernel` (or `make check-event-kernel`)
rather than a hand-rolled `git diff --quiet`.

### 20. Nothing says the backing's schema name is the resolved one  `[low]`

`Backing.Equal` is `crud.SameDataSource`, which is type equality plus `==` over the whole struct
(`crud/executor.go:562`). If `backing.schema` is `Spec.Schema.Name` rather than
`Schema.Resolved().Name`, then a store built from `Spec{DB, Source}` and one built from
`Spec{DB, Source, Schema: Schema{Name: eventpg.DefaultSchema}}` — the same deployment, written two
ways — produce backings that compare **not** `Equal`, and each refuses the other's cursors and
tokens with `ErrCursor`/`ErrWrongStore`. §INV-059's test only compares one schema against another.

Owed: the plan says `backing` holds the resolved name, and `TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot`
gains the empty-vs-explicit-default pair.

### 21. `Close` "sets a flag" with no stated memory model  `[low]`

`event.Store` requires all eight methods to be safe for concurrent use, and the plan says "`Close`
sets a flag, returns nil every time". A plain `bool` read by a concurrent `Append` is a data race
that `-race` will find in the `lifecycle` section only if that section happens to close and call
concurrently; it currently does not. `jobspg` uses `atomic.Bool` for `ready` (`jobs/jobspg/config.go`).

Owed: the plan names `atomic.Bool` for both the closed flag and the ready flag, and the S4 test
`TestCloseIsIdempotentAndClosesNothingItDidNotOpen` closes while an operation is in flight.

### 22. `event/eventpg/go.mod`'s library require version is unspecified  `[low]`

Checklist row 1 names `require github.com/frostgrove/vv` with no version. `check-tidy` reads the
module with a temporary local replace, so any pseudo-version resolves and the check says nothing;
`jobs/jobspg/go.mod` carries `v0.0.0-20260829132449-bc1e4c0b1038`. `scripts/release.sh` and
`make main-deps` are what read it for real, and a satellite whose require is a fiction is the shape
`check-replaces`'s own message calls out.

Owed: the checklist names which version, and by what step it is kept current.

### 23. The narrow-limits run's `MaxKey: 40` sits 13 bytes above `eventtest`'s own floor, and nothing records the dependency  `[low]`

`eventtest.reserve` is `1 + len(markOf(len(sections)-1)) + widestLabel` = `1 + len("s19-") + 8` = 13
with today's twenty sections, so `runIdentity` takes `min(16, (40-13)/2) = 13` bytes and needs 4.
A twenty-first section whose index reaches three digits, or a `widestLabel` bump, narrows that; at
`MaxKey: 40` the run identity shrinks toward the four-byte floor at which
`eventtest` reports 77 000 runs' worth of collision risk against a database nothing truncates.
`eventmemory` runs at the same 40, so the coupling already exists and is simply unwritten.

Owed: a sentence in the plan (or in `eventtest`'s own docs when the kernel is unfrozen) recording
that the narrow-limits `MaxKey` is chosen against `reserve()` and what happens if a section is added.

### 24. The `Log` read door carries no wiring class, so §UC-086 holds at three doors of four  `[medium]`

Owner: **the phase that unfreezes the kernel.** Raised closing GAP-P2-1.

`event.ErrAmbientNotTransaction` is minted by one function, `refuseTransaction` (`event/errors.go:181`),
called from `Repo.Within`, `Repo.Authority` and `Repo.transaction` (`event/repo.go:115,132,147`).
`ReadAll` reaches a consumer only through `Reader.Next` → `refuseRead` → `refuse(err, readDoor)`
(`event/reader.go:48`, `event/errors.go:209-250`), and `refusal.Is` (`event/errors.go:101-107`)
answers **false** for any target that is `inVocabulary` and is not the refusal's own sentinel. So no
outcome a store can return renders that sentinel at the read door: `Refused` gives `ErrRefused`,
`Unclassified` and `NotWritten` give `ErrBackend`, and the bare sentinel gives `ErrBackend` too.

§UC-086's **Then** — "`ErrAmbientNotTransaction` from every door" — is therefore unsatisfiable as
written. Phase 2 answers `event.Failure(event.Refused, …)` from `ReadAll` and asserts the one cause
through `event.CauseOf` at all four doors; the **Must not** ("no fallback to autocommit, at any
door") holds at all four and is measured with a statement counter. `ErrRefused` was chosen over the
two `ErrBackend` routes because those are the retry classes and a wiring error never clears.

Owed: either a `Log`-door wiring class in the kernel — `Reader.Next` asking the store the
transaction question, or an eighth outcome — or §UC-086's **Then** amended in the spec to the
three-plus-one shape the plan now carries.

### 25. A store claiming `Persistence` cannot pass `store failure classification`  `[high]`

Owner: **the phase that unfreezes the kernel.** Raised closing GAP-P2-2, found by driving
`eventtest.Run` rather than by reading it.

`storeFailureSection` runs each of its eight cases twice — through the store and through a wrapping
decorator (`event/eventtest/sections_lifecycle.go:283-307`). Each invocation calls `Factory.New`,
loads `account("a")` and appends one event to it (`probe.go:71,136`, `sections_lifecycle.go:311-317`),
and the `NotWritten` case then asserts the stream holds **exactly one** event
(`sections_lifecycle.go:334-336`). On a backing that survives `New`, the second invocation finds two.
Measured against `eventmemory` over one shared log:

```
eventtest: store failure classification: failed — [outcome not written] injected at the append door
through a decorator that wraps the store's own error left 2 events on a stream that held one, and
the store said the write certainly did not land
```

The `Factory.New` doc comment forbids exactly the `New` that would avoid this — "a store this builds
must not destroy or reset what an earlier one wrote in the same section" (`suite.go:36-40`) — so the
suite's own two requirements are mutually exclusive for a store whose backing is a database.
`eventmemory` passes the section only by violating that comment, and pays for it nowhere because it
declares `Persistence: Unsupported`.

Phase 2's escape is real and is not a fix: `durability` and the `persistence` clause take their
second value from `Factory.Sibling` and fall back to `New` only when no `Sibling` exists
(`sections_lifecycle.go:104-112,247-249`), so `eventpg`'s factory gives every `New` its own migrated
scratch schema and its `Sibling` the equal backing. That certifies both, and additionally certifies
`resumption`'s foreign-cursor clause and `transactions`' chained pair, which ask `this.store()` for a
**second** backing (`sections_resumption.go:66`, `sections_transactions.go:197`).

Owed: `storeFailureSection` reads the stream's length before it injects and asserts it is unchanged,
rather than asserting the literal 1 — or each invocation names its own account.

### 26. No decode-side depth or size bound on a stored payload, and Roadmap §15 assigns it to the wrong package  `[medium]`

Owner: **the phase that unfreezes the kernel.** Raised closing GAP-P2-6.

`docs/roadmaps/Roadmap.md:342-343` inherits "no decode-side depth or size bound beyond the byte cap
(`eventpg` must add its own)" to phase 2. `eventpg` decodes nothing: `payload` is `bytea`, it is
scanned into `[]byte`, and it reaches the consumer as `event.Envelope.Payload` untouched. Decoding is
the declaration's codec inside the frozen kernel, and `event/encodable.go`'s `codecGraphDepth = 1024`
bounds the *type graph* walked at seal time, not the nesting of a stored document at read time.

The store's whole share is the byte cap, and phase 2 delivers it: the read door refuses a row whose
payload exceeds `MaxPayload` before an envelope is built (§UC-093), and the `CHECK` carrying that
number is a fingerprint input (§INV-058). A depth bound over what those bytes decode to needs a store
that parses payloads, which is the one thing an event store must not do.

Owed: a decode-side depth bound at the codec seam in `event/`, and §15's line rewritten to name that
seam rather than `eventpg`.

### 27. The expectation model's `CHECK` expressions are in a spelling `pg_get_constraintdef` never returns  `[medium]`

Raised by the S1 implementation review, measured rather than read. Sharpens §16, which assumed the
expectation side would carry the deparser's own form.

`expectedCheck.expression` (`event/eventpg/schema.go:179-226`) holds source spellings —
`octet_length(family) BETWEEN 1 AND 128`, `version > 0`, `singleton` — and `definition()` renders
them as `CHECK (<expr>)` for both the DDL and the fingerprint. PostgreSQL rewrites all of them.
Against the schema the real statement list deployed on 17.9:

```
events_family_check      => CHECK (((octet_length(family) >= 1) AND (octet_length(family) <= 128)))
events_payload_check     => CHECK ((octet_length(payload) <= 65536))
schema_meta_singleton_check => CHECK (singleton)
events_pkey              => PRIMARY KEY ("position")
```

`BETWEEN` is expanded, every predicate gains a paren layer, and `position` comes back quoted. Not
one of the fourteen check constraints matches `definition()` whitespace-collapsed, so §verify.go
level 3 as specified ("`pg_get_constraintdef` whitespace-collapsed" against the expectation) refuses
an intact schema. S2's `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` fails on its
own control the first time it runs, so this is self-revealing rather than silent — which is why it
is a medium and not a blocker.

The trap is the fix. If S2 answers it by adding a second, catalog-shaped string beside
`expression`, the model then carries two spellings of one constraint that must be kept in sync, and
the fingerprint digests only one of them.

Owed: one spelling per constraint. Either the model carries the deparser's form and the DDL is
rendered from it, or the comparison normalises in exactly one function with a control that an
intact schema passes and each of the fourteen mutations fails. Whichever is chosen changes the
fingerprint, so it is cheapest before `MIGRATIONS.md` and the module pages ship. §16's second half —
no stated PostgreSQL version range — is unchanged and still owed; the statement list uses
`gen_random_uuid()`, `pg_current_xact_id()`, `xid8` and `pg_snapshot_xmin()`, all PostgreSQL 13+,
and nothing in the code, `doc.go` or the plan names a floor.

### 28. `packageLevelState` reports five kinds of mutable state and the control fixture exercises only those five  `[medium]`

`event/eventpg/sources_test.go:178-205` walks the package scope and complains about a package-level
`var` whose underlying type is a map, slice, channel, pointer or array. §INV-060's statement is "no
package-level mutable state exists", which is wider: `var counter atomic.Int64`, `var cache struct{
mu sync.Mutex; byName map[string]string }`, `var seen [some named struct]` and a plain `var total
int64` are all unreported, and the control fixture (`lifecycleFixture`, `sources_test.go:379-420`)
has no case for any of them — so that half of the arm reports through an unfalsified body, which is
P1 §6's shape in a new file.

`ambientReach` has the same edge in the other direction and is narrower than it reads: it names
`log`, `log/slog`, `fmt.Print*`/`Fprint*` and four `os` environment calls. `os.ReadFile`,
`os.Open`, `net.Dial`, `time.Now` and `sql.Open` are invisible to it. That is defensible — "New
performs no I/O" is proved behaviourally by `TestNewAgainstADeadDSNAnswersAStore`, not by the walk —
but it is worth writing down so nobody reads the walk as an I/O check.

Owed: the fixture gains a struct-typed and an `atomic`-typed package-level var and the arm reports
them, or the arm's comment states the five kinds it covers and INV-060 is narrowed to match.

### 29. `MigrationStatements` returns a bare `[]string` and `MIGRATIONS.md` does not exist yet  `[low]`

Restates §11 against the shipped code rather than against the plan. The doc comment
(`event/eventpg/schema.go:300-303`) says the list is "safe inside one transaction, which is what
MIGRATIONS.md says"; there is no `MIGRATIONS.md` (checklist row 16 is S5). A caller who feeds the
eleven strings to a migration tool that runs one statement per file — the ordinary shape — gets a
partially created schema on any failure, and §UC-069's Must-not is then broken by the caller with
nothing in the type to warn them.

Owed: `MIGRATIONS.md` in the change that first tells an operator to use the list, and a sentence in
the module pages stating the obligation in the imperative rather than as a property.

### 30. `validSchemaName` is `jobspg.validSchema` copied byte for byte with no shared home  `[low]`

`event/eventpg/schema.go:78-93` and `jobs/jobspg/config.go:241-256` are the same rule in two
satellites, and GAP-P2-S1-3 shows they share the same defect. Both modules could reach a `utils`
helper without adding a dependency to either. Two copies means the fix has to be made twice and one
of them will be forgotten.

Owed: one PostgreSQL identifier rule in `utils/`, or a recorded decision that each satellite owns
its own and that the two are compared when either changes.

### 31. `quoteLiteral` handles the apostrophe and nothing else  `[low]`

`event/eventpg/migration.go:91-93` doubles `'` and stops. Its only input today is a
`sha256:<hex>` string, so nothing is wrong now; the name promises a general facility and the next
literal somebody passes through it — an operator-supplied comment, a default, a seeded row — is a
backslash away from a different statement under `standard_conforming_strings = off`.

Owed: either the name says what it covers (`quoteFingerprint`), or it emits `E'…'`-safe text and a
test drives a backslash and an embedded quote through it.

### 32. The S1 test binary costs about ten seconds of every `make unit`  `[low]`

`go test -race -count=1 ./event/eventpg/` is 10.4 s for a package that touches no database. The cost
is `importer.ForCompiler(fileset, "source", nil)` in `sources_test.go:47`, which type-checks `crud`,
`event` and their stdlib closure from source, four times per run (two real packages, two fixtures).
It also makes the two source checks depend on the `go` tool being able to resolve the module graph
from inside a test, which a vendored or offline checkout may not do.

Owed: one shared `typeCheck` result per directory rather than one per subtest, or a note in the file
saying the cost is deliberate.

### 33. Two small pins in `event/eventpg` that stop pinning as the package grows  `[low]`

- `sources_test.go:114,220` guard with `len(typed.files) < 4`. Four is the number of non-test files
  today; the plan's file list is eleven. Once it is eleven the guard cannot notice that seven were
  missed, and both checks silently walk a subset.
- `bindsParameters` (`sources_test.go:291-301`) keys on the method name and
  `boundParameterComplaints` reads arguments from `call.Args[2:]`. That is the `(ctx, query,
  args...)` shape; `(*sql.Stmt).ExecContext(ctx, args...)` is `(ctx, args...)`, so a prepared
  statement would have its first bound value skipped, and `conn.Raw` is invisible. Neither shape
  exists in the store today and both are forbidden by the plan — but the check is what enforces the
  ban and it cannot see either.

Owed: the file-count guard becomes the number of files the flow's table names, and the parameter
check either recognises the `*sql.Stmt` shape or reports any statement-issuing call it does not
recognise.

### 34. `doc.go` says the append-only trigger holds "for every writer"  `[low]`

`event/eventpg/doc.go:10-11`. A trigger with `tgenabled = 'O'` is skipped by a session that has set
`session_replication_role = 'replica'`, and `ALTER TABLE … DISABLE TRIGGER` removes it outright.
Both need privilege, and §verify.go level 3 compares `tgenabled` so a disabled trigger is caught at
the next `Prepare` — but "for every writer" is stronger than what the database enforces, and a
package sentence is where an operator forms their model of the guarantee.

Related and worth one line in the same place: the kernel's `MaxKeyBytes` (2048) plus `MaxNameBytes`
(128) fit inside PostgreSQL's btree index-row limit only with room to spare — measured, a row at
both ceilings inserts into the `streams` primary key and the `events` unique index without error. A
later kernel that widened either ceiling would be accepted by `New`, deploy cleanly, and fail at the
first long-keyed append with `54000`. Nothing records the coupling.

Owed: `doc.go` names what the trigger holds against, and the ceiling coupling is written down where
the bounds are chosen.

### 35. One level-3 comparison no mutation can isolate  `[low]`

`catalog.go`'s `compareConstraint` refuses a check constraint whose `connoinherit` is true, and the
S2 case for it (`a check constraint stops at the table it names`) still refuses with that branch
disabled — `pg_get_constraintdef` renders `NO INHERIT` into the definition the token comparison
already reads. The branch is kept because the plan's comparison table names `NOT connoinherit` and
because it states the fault in words rather than as a definition diff, but it is belt over braces
and no test would notice its removal. Measured 2026-09-08 (S2 mutation M16).

Owed: either a case that isolates it — which needs a `connoinherit` that `pg_get_constraintdef` does
not render, and there may not be one — or the branch folded into the definition comparison and the
plan's table amended.

### 36. The spec's schema sketch still spells the byte bounds with `BETWEEN`  `[low]`

`EVENTSOURCE_P2_USECASES.md` §2.1 draws the three tables with
`CHECK (octet_length(family) BETWEEN 1 AND 128)`. S2 authors that expression in the form PostgreSQL
stores it (`>= 1 AND <= 128`), because `pg_get_constraintdef` renders the expansion and level 3
compares against it — one authored expression, no second spelling. The constraint is the same
constraint and the plan records the change, but the spec still shows the other form and the spec is
what a reader reaches for first.

Owed: §2.1's sketch matches the deployed DDL, or says in one line that it is a sketch of the bound
and not of the rendering.

### 37. `Check` reports drift and the store goes on serving it  `[medium]`

*Recorded by the S2 implementation review, 2026-09-08.*

`verify.go:68-92` answers `ErrSchemaMismatch` when the deployed fingerprint moved or the log was
reissued under a running store, and it never touches `this.state`. The store therefore keeps
answering appends and keeps minting cursors bound to the log it verified at start-up, while the
database carries another one — `TestCheckIsAReadinessAnswerAndNamesNoImportance`'s
`a schema migrated again under the store fails` case proves the probe sees it and asserts nothing
about what the store does next. Withdrawing readiness inside a probe is not obviously right either
(a transient failure would then take the store down), which is why this is a decision to make and
not a line to change: a probe that reports is one contract, a probe that fails closed is another,
and the code currently picks the first without saying so.

Owed: `Check`'s contract states whether a detected drift withdraws readiness, and UC-094 says which,
with the reasoning for the one not chosen.

### 38. The live suite's scratch schema names are fixed constants  `[medium]`

*Recorded by the S2 implementation review, 2026-09-08.*

`main_integration_test.go:126-143` deploys `eventpg_shared` and every case names a constant
(`eventpg_s2_gone`, `eventpg_s2_race_0`…), each dropped `IF EXISTS … CASCADE` before use. Two runs
of the tagged suite against one cluster — two branches on one dev container, two CI workers, a
developer running the suite while the gate runs it — destroy each other's schemas mid-test and fail
for a cause that is not the cause. D4 already requires the eventpg suite to run alone for a
different reason (`pg_snapshot_xmin` is cluster-wide), so the constraint exists; nothing in the
suite or the module page states it, and nothing enforces it.

Owed: either a run-unique prefix (D1's conformance factory needs one anyway) or one sentence in the
module page and `MIGRATIONS.md` saying the tagged suite owns the cluster while it runs.

### 39. `Verify`'s three levels are nine statements in autocommit, not one snapshot  `[low]`

*Recorded by the S2 implementation review, 2026-09-08.*

`verify.go:40-62` reads `schema_meta` and then `catalog.go` issues six catalog reads and two
sequence reads, each on the same `*sql.Conn` but each its own snapshot. A concurrent
`ManageSchema` replica committing its (transactional, idempotent) migration between them cannot
produce a wrong *pass* at version 1 — the meta read comes first and DDL is atomic — but the
property is a consequence of statement order rather than of anything the code states, and a version
N whose list alters tables would break it. A `REPEATABLE READ` transaction around the nine reads
would make it structural; it would also make `verify.go` name `BeginTx`, which INV-049's source
check excludes only for `migration.go`.

Owed: either the nine reads run in one read-only `REPEATABLE READ` transaction and INV-049's
exclusion list is extended with a named reason, or the ordering argument is written where the reads
are.

### 40. Two refusal messages name an arbitrary member of an unordered set  `[low]`

*Recorded by the S2 implementation review, 2026-09-08.*

`catalog.go:381` iterates `found` (a map) to find an unexpected trigger and `catalog.go:277-280`
reports `found.unexpected[0]` from rows read with no `ORDER BY`. With two unexpected triggers on
one table, or a rule and a policy at once, the operator is told about a different object on each
run. The refusal is correct either way; which object it names is not reproducible.

Owed: the unexpected sets are sorted before the first is named, or the message names all of them.

### 41. `withMigrationLock` is `jobspg`'s, copied byte for byte, with no shared home  `[low]`

*Recorded by the S2 implementation review, 2026-09-08.*

`event/eventpg/migration.go:179-222` and `jobs/jobspg/retention_migration.go:115-159` are the same
forty-four lines: pin a connection, `pg_try_advisory_lock` in a jittered loop, unlock on a context
detached from cancellation, treat a false unlock as an error and discard the connection. The
duplication is deliberate — two satellite modules share no home and [[D-033]] is why — but the two
must now be kept in sync by memory, and a fix to one is invisible to the other. Companion to §30
(`validSchemaName`, the same shape).

Owed: one decision covering both — either a shared home for the pinned-advisory-lock helper in a
package both satellites already import, or a line in each naming the other as its twin.

### 42. A schema whose `schema_meta` row is gone is stamped with a description of a schema this list did not build  `[low]`

*Recorded by the S2 implementation review, 2026-09-08.*

Statement ten inserts `ON CONFLICT (singleton) DO NOTHING`, so it records this build's fingerprint
whenever the row is *absent* — including over tables another expectation created and someone then
deleted the row from. Statement eleven reads back what statement ten just wrote and passes. Level 3
catches the differences it can see (the check operands, the columns, the triggers), so the loud
path holds and the store still refuses to serve; what is left behind is a `schema_meta.fingerprint`
that is false, which is the value `Fingerprint()` is exported for an operator to compare against
during an incident. S1's GAP-P2-S1-1 closed the *re-stamping* half of this; the *first*-stamping
half over a foreign schema is what remains.

Owed: statement ten either refuses when the tables it describes already exist without a meta row,
or `MIGRATIONS.md` says that deleting the meta row of a deployed schema makes the next migration's
record untrustworthy.

### 43. Level 3 runs at `Prepare` and nowhere else, and nothing tells a deployment it may run it again  `[low]`

*Recorded by the S2 implementation review, 2026-09-08.*

`Check` is levels 1 and 2 by decision (level 3 is eight catalog statements and a probe runs on the
deployment's schedule). The consequence is that a unique constraint dropped, a trigger disabled or
a policy added **under a running process** is invisible until the next restart — and a dropped
`events_stream_version_key` is two writers both told they won. `Verify` is exported and composable,
so a deployment can schedule a full re-verification today; nothing says so, and the module page is
where it would.

Owed: the module page names `Verify` as the periodic full check and says what `Check` deliberately
does not cover.

### 44. §UC-086's fourth door is measured through a test-owned `ReadAll` until S4 lands the real one  `[medium]`

*Recorded by the S3 implementation, 2026-09-08.*

`event.Store` is eight methods and `event.Open`, `event.Bind` and `event.Read` take nothing
narrower, so S3 — which delivers six of them — cannot assemble the kernel around the store at all
without the two read doors. `main_integration_test.go` therefore carries a `kernelStore` that embeds
the real store and reads the deployed rows with the test's own statement, and
`TestAnAmbientNonTransactionRefusesAtAllFourDoors` gets real evidence at four of its five
assertions (`Repo.Within`, `Repo.Authority`, `Repo.Load` — asserted to refuse before it reaches a
read — `Repo.Append`, and `Store.Append` at the store's own door) and scaffolded evidence at the
fifth, `event.Read(store, "").Next`. What is measured there today is the cause the door carries and
the zero statements behind it; that the store's **own** `ReadAll` answers `Refused` rather than one
of the two retry classes is not yet proved by anything.

Owed: when S4 lands `read.go`, delete `kernelStore` and re-point the fourth door at the store's own
body — the test name and its assertions do not change, only what answers them.

**Closed by S4, 2026-09-08.** `kernelStore` is deleted, `boundRepo` binds the store itself, and the
fourth door is `event.Read(store, "").Next` over the store's own `ReadAll`. The read-counter
assertion is now the statement counter, so what it measures is unchanged.

### 45. `ORDER BY record.ord` has no behavioural mutation on PostgreSQL 17.9  `[low]`

*Recorded by the S3 mutation campaign, 2026-09-08.*

Removing the clause left `TestABatchLandsDenseAscendingAndAtOneInstant` green: this planner's
`VALUES` scan happens to reach the `INSERT` in list order, so the positions still ascended in the
order the caller listed its changes. The clause is a guarantee neither a `VALUES` scan nor an
`unnest` makes, so it stays, and the test now pins it as statement text — which catches the
mutation but pins a rendering rather than a behaviour.

Owed: nothing, unless a batch shape is found where the two orders differ. If one is, the pin becomes
an ordinary assertion over positions and the text check goes.

### 46. The untagged `eventpg` suite now costs about 36 seconds of every `make unit`  `[low]`

*Recorded by the S3 implementation, 2026-09-08, extending §32.*

S1's two source checks type-check the package with `go/types` and the source importer; S3 adds four
more, and each `typeCheck(t, ".")` re-imports the whole dependency graph from source. Eight of those
per run is about 36 seconds where S1 alone was about ten.

Owed: one memoised `typedPackage` per directory shared by all six checks, or a single walk that
collects every complaint kind at once.

### 47. `eventpg`'s own sentinels are reachable by `errors.Is` through the `Refused` door  `[medium]`

*Recorded by the S3 implementation review, 2026-09-08.*

§INV-062 and the plan (§config.go, plan line 592) both say the three sentinels "travel as an
`event.Failure`'s *cause*, reachable through `event.CauseOf` and by neither `errors.Is` nor
`errors.As` through a kernel sentinel". That is false for the `Refused` outcome, and the kernel is
why: `failure.refuse` maps `Refused` to `newRefusal(ErrRefused, causeAsWrap(this.cause), this.cause)`
(`event/errors.go:238`), which promotes the cause to the refusal's **wrap**, and `refusal.Is`
answers any target that is not in the kernel vocabulary off that wrap (`event/errors.go:101-107`).
Measured with a throwaway probe against the live database: an append through an unprepared store
answers `errors.Is(err, event.ErrRefused) = true` **and** `errors.Is(err, eventpg.ErrNotReady) =
true`. The same holds for `errAmbientNotTransaction` at the three refusing doors.

No behaviour is wrong — the promotion is the kernel's deliberate design for a policy refusal, and
it is arguably useful — but a claim a reviewer can falsify in one call is a claim the next agent
will trust. Nothing else in `eventpg` depends on it either way.

Owed: either the sentence is corrected in the use case and the plan (the cause of a `Refused`
failure is matchable by `errors.Is`, by kernel design, and only a *kernel* sentinel is unreachable),
or the store passes `nil` as the cause and gives up `event.CauseOf` at those doors — which
`TestAnAmbientNonTransactionRefusesAtAllFourDoors` currently rests on, so the first is the likely
answer.

### 48. `int32(record.Revision)` is an unguarded narrowing at the one door that guards its neighbour  `[medium]`

*Recorded by the S3 implementation review, 2026-09-08.*

`append.go:52` binds `int32(record.Revision)` where `event.Record.Revision` is an `int`, two lines
below a guard that refuses an `Expected` above `math.MaxInt64` rather than letting it wrap
(`append.go:45`). A revision of `1<<32 + 1` is bound as `1`: the row passes `CHECK (revision > 0)`,
the append returns **nil**, and every later replay folds the fact through the wrong upcaster with no
error anywhere. Through `event.Repo` this is unreachable — the revision is an index into the fact's
declared chain — but `Store.Append` is exported and is called directly by the conformance suite and
by anything decorating the store, and `event/store.go` puts the validation burden on the caller
("already inside every bound the store published"), which is exactly the naive contract this
framework forbids.

Owed: refuse a revision outside `1…math.MaxInt32` with `event.Failure(event.Conflict, …)`-style
refusal or a `NotWritten` before the statement is built — one `if`, beside the `Expected` guard —
and a case that appends such a record directly at the store's door.

### 49. `causeOf` carries a retryable arm the PostgreSQL table can never produce  `[low]`

*Recorded by the S3 implementation review, 2026-09-08.*

`classify.go:73` lists `errs.CodeUnavailable` among the codes that get an `errs.KindRetryable`
fault, but `errs/sqlerr/postgres.go:5-18` maps thirteen SQLSTATEs and none of them to
`CodeUnavailable`, so for `postgresDialect` the arm is unreachable. It is harmless and reads as
defensive, but it is a fourth constant a reader must check against a table to discover it is dead.

Owed: drop it, or a comment naming the dialect entry that would produce it.

**Closed 2026-09-08, not by this backlog but by §GAP-P2-S3-1's close criteria**, which required the
whole rule to be walkable by a test and therefore had no room for an arm that cannot be reached. The
constant is gone and `classify.go`'s comment says why there are three codes and not four.

### 50. The S3 checkpoint transcript is of a different command than the one pasted above it  `[low]`

*Recorded by the S3 implementation review, 2026-09-08.*

The phase-5 block asserts `= 16` over a sixteen-name pattern; the transcript beneath it shows
`… = 15` with a parenthetical that the savepoint case "is listed above and runs in the same suite".
Both are true of this tree — I ran the block as written and the count is **16** — so nothing is
wrong except that the evidence does not correspond byte for byte to the command it evidences, which
is the property the counting arms exist to have.

Owed: re-run the pasted block and paste that transcript, or paste the fifteen-name command.

### 51. One assertion in the batch test cannot fail  `[low]`

*Recorded by the S3 implementation review, 2026-09-08.*

`append_integration_test.go:222` asserts `row.position > rows[index-1].position` over rows the
helper read with `ORDER BY position` (`main_integration_test.go:554`), so it holds for any log at
all. The real work in that test is done by the payload sequence and the dense versions, which do
catch a reordering, and by the statement-text pin §45 records — but an assertion that cannot fail
reads as evidence and is not.

Owed: compare positions against the caller's order (a map from payload to position), or delete the
clause and say in the test that the payload sequence is what pins the order.

### 52. `Append` asks the ambient-executor question twice per call  `[low]`

*Recorded by the S3 implementation review, 2026-09-08.*

`append.go:39` calls `Store.Transaction(ctx)` for its refusal and discards the authority;
`onExecutor` (`executor.go:62`) then calls `bound(ctx)` again, so every append walks
`crud.ExecutorFor`'s binding chain twice and builds an `event.Authority` nobody reads. It is
correct — the context cannot change between the two — and the duplication is what keeps the refusal
at the door where the plan wants it, but one resolution handed to `onExecutor` would be one
mechanism rather than two.

Owed: nothing, unless the door order changes; then resolve once and pass the result.

### 53. §INV-062 is unsatisfiable as written at the `Transaction` door  `[medium]`

*Recorded by the S4 implementation, 2026-09-08.*

§INV-062 says everything the eight methods return is "nil, a bare context error, or an
`event.Failure` carrying one of the seven outcomes". `Store.Transaction` cannot answer that shape:
the kernel mints `ErrAmbientNotTransaction` in `refuseTransaction` (`event/errors.go:181`) and hands
the store's error through as the refusal's cause, which `event.CauseOf` and `errors.Is` then read —
and an `event.Failure` "deliberately does not unwrap to its cause" (`event/outcome.go`), so a store
that classified there would make `errors.Is(cause, …)` false at three of the four doors.
`eventmemory.Transaction` (`event/eventmemory/transaction.go:61-70`) returns a bare package error for
the same reason, so both stores agree and the invariant's wording does not.

`TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure` therefore walks the three
operating doors plus `Close` for the stated shape and asserts `Transaction`'s own shape separately,
with the reason in the test.

Owed: §INV-062 amended to "the three operations and `Close`", with `Transaction`'s third answer named
as the kernel's own contract. No code changes.

### 54. §2.6's `OVERRIDING SYSTEM VALUE` boundary is one statement too narrow  `[medium]`

*Recorded by the S4 implementation, 2026-09-08, from a live measurement on PostgreSQL 17.9.*

§2.6 and §UC-098 control (b) bound the watermark's guarantee to writers that "let the identity column
draw the position", and name the excluded shape as "a writer that draws a position in one statement
(`SELECT nextval(...)`) and inserts it in a later one". Measured: **`nextval()` assigns a transaction
id.** A transaction that draws a position with `nextval` and inserts it later with `OVERRIDING SYSTEM
VALUE` is therefore covered like any other writer — the walk stops at its gap and delivers the row
after it commits.

The excluded shape is narrower and is about the *transaction*, not the statement: a position drawn in
a transaction that has since **ended**, inserted by a later one. `TestAForeignWriterIsNotSkippedAndThe`
`OverridingWriterIs` now drives both halves, so the boundary is a test result on both sides.

Owed: §2.6's fourth paragraph and §UC-098 control (b) restated in the spec, and
`docs/modules/{en,ru}/eventpg.md` (S5) written to the measured boundary rather than the stated one.

### 55. A read cancelled mid-statement renders as `ErrBackend`, and the arm S4 removed is reachable  `[medium]`

*Recorded by the S4 implementation review, 2026-09-08.*

`event/eventpg/read.go:80,162` classify every failure of an issued read as
`event.Failure(event.Unclassified, …)`, which the kernel's read door maps to `ErrBackend`
(`event/errors.go:244-250`). A context cancelled **while** `QueryContext` is in flight therefore
reaches the caller as "the store failed, retry" rather than as the cancellation: the kernel's own
rule is that "a cancellation travels as itself or not at all" (`event/errors.go:88-90`), and
`refusal.Is` answers **false** for `context.Canceled` and `context.DeadlineExceeded` always
(`event/errors.go:101-104`), so the caller cannot see it through `errors.Is` at all — only through
`event.CauseOf`. A projector shutting down reads a backend fault on its last call.

S4 contract change 2 justifies removing the arm with "a `ctx.Err()` arm on the failure path is
unreachable — the door already refused a done context before anything was issued — so it would be a
branch no test can falsify". That is wrong about the code: `opened` (`executor.go:33-48`) checks
`ctx.Err()` before the statement, which says nothing about a context cancelled during it, and a test
can reach it by cancelling from a second goroutine or by cancelling under a slow statement. The
removal may still be the right answer — `Append`'s own cancellation rule (§UC-083) is deliberately
"answered by the rule and not by one outcome" — but the reason recorded for it is not one.

Owed: either an arm that lets a cancelled read travel as the bare context error with a live test
that cancels mid-statement, or S4 change 2 restated with the real reason (the reads follow the same
classification rule as the append) plus a test that pins the current answer.

### 56. `errNoRow` carries the statement text into a returned error value  `[low]`

*Recorded by the S4 implementation review, 2026-09-08.*

`event/eventpg/read.go:277` builds `fmt.Errorf("%w: %s", errNoRow, statement)`. The statement is one
of two package constants (`SELECT pg_current_xact_id()::text`,
`SELECT pg_snapshot_xmin(pg_current_snapshot())::text`), so no caller data and no identity leaks, and
the kernel renders the sentinel and nothing of the cause (§INV-025) — but SQL text inside a returned
error is the shape the error-hygiene rule exists to keep out, and the next statement added there may
not be a constant.

Owed: name the statement rather than quote it (`"the transaction id mint"` / `"the snapshot floor"`),
or drop the detail.

### 57. `fetch` re-parses the horizon once per row and silently keeps the last  `[low]`

*Recorded by the S4 implementation review, 2026-09-08.*

`event/eventpg/read.go:184` runs `strconv.ParseUint(horizon, …)` inside the row loop, so a page of
`MaxRead` rows parses the same constant up to 256 times, and if two rows ever disagreed the last one
would win with nothing said. The `LEFT JOIN LATERAL` makes them equal by construction today, which is
why this is `[low]` rather than a correctness item.

Owed: parse the horizon once, on the first row, and refuse a page whose rows disagree about it.

### 58. Both read doors ask the ambient-executor question twice per call  `[low]`

*Recorded by the S4 implementation review, 2026-09-08. Extends `## P2` §52 from `Append` to all
three operating doors.*

`Store.opened` calls `Store.Transaction` (`executor.go:44`), which calls `bound`; `onExecutor` then
calls `bound` again (`executor.go:85`). `ReadStream`, `ReadAll` and `Append` therefore resolve the
context's executor twice per call and build an `event.Authority` that is discarded. It is cheap and
cannot disagree with itself today — `crud.ExecutorFor` is a context lookup — but it is two answers to
one question, which is the shape §52 already records.

Owed: resolve once and pass the result, the same fix §52 names.

### 59. `eventtest` never walks the log while a lower position is uncommitted, so a cursor that skips an in-flight position is invisible to it  `[high]` — **CLOSED by P3 S2, 2026-09-08**

*Recorded by the S5 implementation, 2026-09-08. Owner: the phase that unfreezes the kernel. Extends
`## P1` §1 with a measured instance.*

The S5 mutation harness runs six decorators over the real `*eventpg.Store` through `eventtest.Run` in
a subprocess and asserts that the named section reports each one. Five were caught first time. The
sixth — **the defect `eventtest`'s own inventory names "returns its newest position as a cursor while
a lower one can still commit", paired with `resumption`** — was written literally: after a normal
`ReadAll`, answer a cursor at the highest position **the page's own window returned** rather than at
the settled watermark, so the walk steps over a gap a writer still holds. That store **passed all
twenty sections**, measured:

```
a store that newest-position-cursor passed the conformance suite, so the suite certifies a defect
the resumption section is supposed to report
```

The reason is structural, not incidental. `resumptionSection`'s `lateWriter`
(`event/eventtest/sections_resumption.go:108-131`) opens a transaction, appends `late` inside it,
appends `beside` **outside** it, and then **commits before returning**. Every `ReadAll` the section
issues — `tail`, the first `reader.Next`, the resumed `from`, the closing `walkLog` — runs with the
log quiescent. No other section walks the log while a lower position is uncommitted either. So a
cursor that differs from the watermark **only** across an in-flight gap is behaviourally identical to
a correct store everywhere the suite looks.

`eventtest`'s own proxy for the defect, `unsafeCursors` (`event/eventtest/defects_read.go:121-129`),
does not have this problem because it is a different shape: it delivers half a page and answers the
whole page's cursor, which loses events in a quiescent log too. What S5 ships is the same shape —
a cursor at `max(position)` over the whole log, which loses everything past the page bound — and
`resumption` does report it. That keeps the harness honest about what it proves, and it does **not**
close this entry: the in-flight variant is still undetected.

Owed, and it is a change to `event/eventtest` and therefore frozen by §INV-061: a case in
`resumptionSection` that walks the log **while** the late writer's transaction is still open, asserts
the walk delivers nothing at or beyond the held position, commits, and asserts the next walk from the
persisted cursor delivers it. That is the one assertion that separates a settled watermark from the
newest position, and the suite that exists to certify store implementations does not make it.

**Closed by phase 3 S2.** `heldWriter` replaces `lateWriter`: the late writer's transaction is held
**open** across the resumed walk, the section asserts the walk returns nothing that transaction wrote,
commits, and asserts the walk resumed from the cursor persisted during the hold returns it
(`event/eventtest/sections_resumption.go:acrossTheFlight`). The defect the entry describes is now a
row of the eventpg mutation harness — `in-flight-newest-position`, a decorator answering the highest
position its own read's query returned rather than the settled watermark — and
`TestTheNewestPositionCursorNowFailsResumption` drives it with the unmodified store as the control.
Measured both ways: against the section as it stood, that store passed all twenty sections; against
the restructured one it fails `resumption`.

### 60. The mutation harness runs the narrow-limits configuration only  `[low]`

*Recorded by the S5 implementation, 2026-09-08.*

`event/eventpg/mutation_integration_test.go:mutatedRun` points every subprocess at
`TestTheStoreSatisfiesTheContractAtNarrowerLimits`, because `StreamPage: 4, MaxBatch: 2, MaxRead: 3`
reaches every paging boundary in a fraction of the writes the defaults need — the `payload ownership`
section alone appends `max(StreamPage, MaxRead) + 1` events, which is 257 at the defaults and 5 at
the narrow limits. Seven subprocess runs cost about nine seconds that way.

Nothing is known to be caught only at the defaults, and the defaults are exercised by
`TestTheStoreSatisfiesTheContract` in the parent process. Still, a mutation that only bites at a page
of 256 would not be seen by this harness.

Owed: run each mutation at both configurations, or state which sections' assertions are
limit-independent and why one run is enough.

### 61. The shared-schema audit runs fifth of the tagged suite, not after it, and its own guard cannot fail  `[medium]`

*Recorded by the S5 implementation review, 2026-09-08.*

`EVENTSOURCE_P2_PLAN.md` §S5 change 3 says the audit "audits the shared schema every other case of
the live suite wrote to", and the coverage matrix proves INV-047 with
`TestTheAuditOverTheWholeSchemaHolds` marked *(post-suite)*. It is not post-suite. Go registers
tests in file order, and `go test -tags=integration -list '.'` prints
`TestTheAuditOverTheWholeSchemaHolds` **fifth**, before `TestTheStoreSatisfiesTheContract` and before
every test in `read_`, `schema_`, `transaction_`, `uncertainty_` and `watermark_integration_test.go`.
`sharedSchema(t)` has twenty-five callers across five files; four of them — all in
`append_integration_test.go` — run before the audit. The twenty-one that do not include every
rollback, every savepoint and every injected-failure case, which are exactly the writers most likely
to leave a stream row and its events disagreeing.

The subtest's own guard cannot fail either: `audit_integration_test.go:96-98` refuses
`held.streams == 0 || held.events == 0`, and `raceInto` on line 90 has just written three streams
and thirty-six events into that schema, so the condition is unreachable whatever the rest of the
suite did or did not write.

INV-047 still has real evidence — the contended-load subtest, and the ~45 conformance schemas the
factory audits in `auditThenDrop` under the concurrency section's load — so this is the claim being
wider than the measurement rather than the measurement being absent.

Owed: either run the shared-schema audit from `TestMain` after `m.Run()`, or from a test whose name
sorts last, or drop the "every other case" claim from the plan and the coverage matrix and say what
the subtest actually audits. Whichever is chosen, the `streams == 0 || events == 0` guard should
become an assertion that can fail — a recorded lower bound on what the rest of the suite writes, or
nothing at all.

### 62. Both module pages promise "PostgreSQL 14+" and only 17.9 was measured  `[medium]`

*Recorded by the S5 implementation review, 2026-09-08. Sharpens `## P2` §16.*

`docs/modules/en/eventpg.md:16` and `docs/modules/ru/eventpg.md:16` both open with "a complete
`event.Store` over PostgreSQL 14+". Nothing in the repository measures any version but 17.9
(`select version()` on the gate's cluster), and no test, check or CI matrix pins a lower bound.

The claim is not free. Level 3 compares `pg_get_constraintdef` output token by token against a Go
literal whose spelling was chosen to match what the planner returns — `schema.go:187-193` says so in
as many words ("BETWEEN is expanded by the planner and `pg_get_constraintdef` renders the
expansion") — and compares `pg_proc.prosrc` against a body the model carries. Both are rendering
decisions PostgreSQL is free to change between majors. A consumer who reads "14+", deploys on 14 or
15 and meets a rendering difference gets `ErrSchemaMismatch` at `Prepare` against a schema that is
correct in every way that matters. It fails closed and loudly, which is why this is `[medium]` and
not higher, but the page told them the version was supported.

`## P2` §16 already records "no PostgreSQL version range is stated"; what changed is that S5's docs
now state one, unmeasured.

Owed: either run the tagged suite against the oldest claimed major in CI and keep the row, or say
"measured on 17.9" and name the features the store needs (`pg_current_xact_id`,
`pg_snapshot_xmin(pg_current_snapshot())`, `xid8`, `gen_random_uuid()` — all PostgreSQL 13) with the
catalog-text comparison called out as the thing that has not been checked across majors.

### 63. `sectionsThatFailed` can panic on a negative slice index  `[low]`

*Recorded by the S5 implementation review, 2026-09-08.*

`event/eventpg/mutation_integration_test.go:140-151` finds the first `eventtest: ` in a line and then
slices at `strings.Index(name, ": failed")` without checking for `-1`. The guard above it only
establishes that `": failed"` occurs **somewhere** in the line, not that it occurs **after** the
`eventtest: `. A child line carrying `: failed` before an `eventtest: ` — a quoted message, a
`t.Fatalf` that embeds earlier output, a future change to `eventtest`'s wording — panics the helper
with `slice bounds out of range [:-1]` on the exact path that is trying to explain why a mutation was
not caught.

Owed: `at := strings.Index(name, ": failed"); if at < 0 { continue }`.

### 64. The `Conflict` hook is honest only because the suite injects at the stream's current version  `[low]`

*Recorded by the S5 implementation review, 2026-09-08.*

`conflicting.Append` (`event/eventpg/conformance_integration_test.go:301-304`) produces
`event.Conflict` by forwarding the caller's request at `Expected + 1`. That is a real zero-row answer
from the real admission predicate — but only while the suite injects at the version the stream is
actually standing at. If `storeFailureSection` ever injected at a version the stream has already
left by one, `Expected + 1` would equal the stream's version and the append would **succeed**, and
the hook would produce a write where the case asked for a failure.

It fails loud rather than silent (the case would report the section `failed`), and the six other
hooks do not have the property, so this is `[low]`. It is still a dependency on frozen suite
internals that nothing records: the hook's correctness is a fact about
`event/eventtest/sections_lifecycle.go`, not about `eventpg`.

Owed: mint the conflict from a version the stream demonstrably is not at — read the store's own
`ReadStream` for the current version and add one, or append a decoy first — or write the dependency
down beside the hook.

### 65. The `failing` wrapper overrides three methods, so five of the eight cannot be mutated  `[low]`

*Recorded by the S5 implementation review, 2026-09-08.*

`failing` (`event/eventpg/conformance_integration_test.go:161-206`) embeds `*Store` and overrides
`Append`, `ReadStream` and `ReadAll`. `Capabilities`, `Limits`, `Backing`, `Close` and `Transaction`
are promoted from the real store, so no defect in those five can be expressed by `mutated()` and none
of the sections that read them — `binding`, `bounds`, `lifecycle`, `shared backing`, the whole
`transactions` authority half — has a mutation proving it would report one.

The six shipped mutations were chosen deliberately and cover the three data doors, so this is a
statement about what the harness does **not** claim rather than a defect in what it does. It matters
because §S5's argument is "eventtest detects 27 of 165 of its own assertions, so the harness is what
makes a green run evidence" — and the harness currently makes that argument for three doors of eight.

Owed: either extend the wrapper so a mutation can answer a wrong `Backing()`, a wider `Limits()` or a
`Transaction` that reports an authority of another backing, or state in the plan and in FL-037 which
five methods the harness does not cover and what covers them instead.

---

## P3 — `event/projection`, checkpoints, catch-up

Recorded under the 2026-09-08 policy: `[medium]` and `[low]` are scheduled here and left alone until
the core mechanics of all five phases are in. Everything below was raised by the phase-3 use-case
audit (Round 1, [`EVENTSOURCE_P3_USECASES_GAPS.md`](EVENTSOURCE_P3_USECASES_GAPS.md)) against
[`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md). The seven blocking findings
are in that file and are **not** repeated here.

### 1. A handler has no spelling for a permanent failure, and `jobs`' spelling silently means the opposite  `[medium]`

§3.4's table sends the handler's own error to `Spec.Classifier`, whose default (`projection.Classify`)
calls the four history-class sentinels `Permanent` and **everything else `Retryable`**. A handler that
knows its failure will never clear — a foreign key that does not exist, a row shape this build cannot
write — has no way to say so except supplying a whole `Classifier` for the projection.

The same repository already has the idiom: `jobs.Permanent(err)` / `jobs.IsPermanent(err)`
(`jobs/handler_error.go:27,34`), reached by `classifiedHandlerDisposition` through an unexported
marker interface. A consumer who uses both subsystems will write `jobs.Permanent(err)` in a projection
handler; `projection.Classify` does not know the marker, so it answers `Retryable` and the event is
re-applied ten times over roughly four minutes of backoff before halting anyway. Same terminal state,
wrong four minutes, and nothing says why.

Phase 3 also re-spells three more `jobs` mechanisms without recording that it is doing so:
`projection.Backoff{First, Max}` against `jobs.BackoffPolicy` (`jobs/policy.go:20`, option
`jobs.RetryBackoff`), `Spec.Attempts int` against `jobs.RetryLimit` (`jobs/outcome.go:21`) and
`AttemptOrdinal`/`RetrySpent` (`jobs/attempt.go:8,21`), and `Quarantines` against the permanent-failure
disposition path (`jobs.PermanentFailureDisposition`, `jobs/disposition.go`). The duplication is
*justified* — §11.3 forbids `event/projection` the `jobs` closure, and a projection that dragged the
whole job runtime into every consumer of the root module would be worse — but the justification is
nowhere in the document, and the three decision docs §11.2 owes do not include it.

Owed: a `projection.Permanent(err)` (or an exported error the classifier honours) so the common case
needs no classifier, `Classify` documented as honouring it; and one paragraph — in the decision doc
about the supervised runner — naming `jobs.BackoffPolicy`, `jobs.RetryLimit` and `jobs.Permanent` and
saying why phase 3 re-spells rather than imports them.

### 2. §1.3(2)'s prefix stability is load-bearing, has no invariant, and §UC-108 cites the wrong one  `[medium]`

§1.3(2) — "Re-reading from one cursor is stable in its prefix … only the *number* of them may grow" —
is what makes a crash replay "the same work and never a different subset", and §UC-108's **Must not**
leans on it directly ("must not hand the second delivery a different subset — a re-read from one
cursor is stable in its prefix and may only grow (§INV-066)"). §INV-066 says nothing of the kind: it
is "a checkpoint is a store-minted cursor, and never a position". No invariant in §8 states prefix
stability and no `eventtest` section proves it, so a consumer-facing assumption in the list titled
"What a consumer may assume" is the one item on it with no falsification.

It is derivable from §INV-035 plus ascending positions, which is why this is `[medium]`: the property
is true of both shipped stores. What is missing is that a third store could satisfy §INV-035 and still
answer a differently-shaped page from one cursor, and nothing would report it.

Owed: fix §UC-108's citation, and either state the property as an invariant with a `resumption`-side
case (read a page, re-read from the *same* cursor after more commits, assert the second answer starts
with the first) or say it is a corollary and name the two invariants it follows from.

### 3. §1.3(3) restates §INV-054 in its absolute form and drops the limit phase 2 measured  `[medium]`

§1.3(3) tells a projection author that "every event at a position ≤ `H` that is or ever becomes
visible has already been delivered by that walk", with no qualification. `## P2` §2.6 states the one
writer shape that is **not** covered — a writer that draws a position with `SELECT nextval(...)` in
one statement and inserts it with `OVERRIDING SYSTEM VALUE` in a later one can hold an unassigned
transaction across the bound and be skipped — and `event/eventpg/watermark_integration_test.go:706`
drives it, with a failure message that says the module page would then overstate the limit.

§UC-098 admits other writers to the `events` table exist, so this is a real deployment shape, and §1.3
is the chapter a projection author reads rather than the eventpg module page.

Owed: one sentence in §1.3(3) scoping the theorem to writers that let the identity column draw the
position, pointing at the eventpg module page for the boundary.

### 4. No pass timeout, and §UC-118's two "must not"s can conflict  `[medium][immediate-ish]`

Nothing in §3, §9.2 or §UC-118 bounds a pass. `runtime.PeriodicSpec` next door carries a `Timeout`
for exactly this (`runtime/periodic.go:34`, defaulted to the interval). §UC-118 then asks for two
things that a slow handler makes mutually exclusive: "must not abandon a page between the handler and
the save when it was given the chance to finish" and "must not ignore the drain deadline, which would
hold the whole supervisor past its grace". The spec never says which wins, and it never says what
context the handler runs on — the runner's (cancelled at `Supervisor.Stop` after the drain grace,
so a handler must be interruptible and an `InUnit` commit can be cancelled mid-flight) or a detached
one.

Consequence when it is guessed wrong: `Supervisor.Stop` returns `ErrDrainDeadline` on every deploy,
or an `InUnit` pass is cancelled between the handler and the commit and the save answers
`ErrUncertain` on a context that is already dead — so §3.4's bounded resolution (`Load` once) cannot
run, and the process exits without knowing whether the advance landed. Nothing is lost (the next start
resumes from the checkpoint), which is why this is `[medium]` rather than blocking.

Owed: say what context `Apply` receives and whether it is cancelled at shutdown; decide whether
`Spec` gains a pass timeout; and resolve §UC-118's two must-nots into an order (drain waits up to the
deadline, then the pass is abandoned and redelivered).

### 5. `Progress.Applied` and `Progress.At` have no defined semantics, and both are persisted columns  `[medium]`

§1.2 and §9.1 give `Progress` three fields and define one. `Applied uint64` is not said to count
envelopes or pages, nor whether it is cumulative across restarts (the `applied bigint` column is
persisted, so a fresh process must either read it back and continue or reset it to zero — two
different dashboards). `At time.Time` does not say whose clock: the projection has no injected clock
in `Spec`, while the row's own `updated_at` comes from `statement_timestamp()`, so one row carries two
instants from two clocks and §3.1 does not say which `Load` answers.

Phase 1 made an injected clock a store obligation ("records the instant from its own injected clock");
a projection that reads `time.Now()` is the first place in this subsystem that does not.

Owed: define `Applied` (unit and lifetime) and `At` (which clock, and whether `Load` answers the
stored value or the column), in §1.2 and in the `eventpg` column table.

### 6. What a store failure on `Save` retries — the save or the whole pass — is unstated  `[medium]`

§3.4's `ErrBackend` row says "Back off and try again, without limit", for a failure that can arrive at
the **read** or at the **save**. At the read that is unambiguous. At the save it is not: does the next
attempt re-issue the save alone, or re-run the pass — which in `AfterApply` means calling `Apply`
again for a page whose rows are already committed, and in `InUnit` means re-running handler and save
inside a fresh unit (which is correct, since the unit rolled back)?

The two modes plausibly want different answers, and a handler with a side effect outside its
transaction sees the difference on every database hiccup.

Owed: one row per mode in §3.4, or a sentence in §3.2 saying the retry unit is the pass in `InUnit`
and the save in `AfterApply`.

### 7. `New`'s refusal set is not enumerated, and "a `Unit` beside `AfterApply` is refused" has no case  `[medium]`

§9.2's `Spec` has fifteen fields and §3/§7 name three refusals: `InUnit` with a nil `Unit`, `InUnit`
with a checkpoint store that does not claim transactions (§UC-107 a, b), and a `Log` that is also a
`Store` (§UC-115). Unstated: a nil `Log`, `Handler` or `Checkpoints`; a `Name` that fails
`checkName`; `OnPermanentFailure: Quarantine` with a nil sink (§UC-110's **Must not** says it must be
refused but no case says where); `Backoff.First > Max`; a negative `Idle`, `Attempts` or `Tolerate`;
an `Advance` value outside the enum (`Advance.Valid()` exists and nothing says who calls it).

Separately, §9.2's comment refuses a `Unit` **beside** `AfterApply`. No use case covers it, and it
refuses a wiring that is otherwise reasonable — a handler whose writes want one transaction while the
checkpoint deliberately stays outside it (a read model in another database, §12.7's case). Either the
refusal is right and the argument is owed, or the wiring is legitimate and the refusal is wrong.

Owed: the full refusal list in §9.2 with one case covering it (the shape §UC-107 already uses), and
an argument for refusing `Unit` under `AfterApply`.

### 8. Two schema managers over one schema in one process  `[medium]`

§9.3 gives `eventpg.CheckpointSpec` its own `SchemaManagement`, and §9.3's closing line says "`Store`
and `Checkpoints` over one schema are two resources at one schema version, so a deployment migrates
once and each verifies at its own `Prepare`". What happens when **both** are configured
`ManageSchema` in one process is not said: two `Prepare` calls, each holding the advisory lock, each
believing it owns the v1→v2 transform. §UC-073 covers two *replicas* racing; §10(8) drives that at the
new version. The in-process pair is a different order of operations (the same `*sql.DB`, possibly
concurrent under `fx`) and no case covers it.

Owed: say whether `Checkpoints.Prepare` may migrate at all or only verify, and add the in-process pair
to §UC-073's coverage or to a new case.

### 9. `CheckpointCapabilities` has no `SharedBacking`, but §UC-100 is exactly that capability  `[medium]`

`event.Capabilities` distinguishes `Persistence` (survives the process) from `SharedBacking` (two
store values over one backing are one store — §UC-054, and what a restart actually exercises).
`CheckpointCapabilities` carries two supports and drops `SharedBacking`, and §9.4 then gates the
`Sibling` hook — "a second value over the same backing, which is what a restart is" — on
`Persistence`. The two are not the same claim, and §3.1 says `eventmemory.Checkpoints` supplies a
working `Sibling` while declaring `Persistence: Unsupported`, so the memory store's cross-value
behaviour — the behaviour the `InUnit` proof without a database rests on — is never certified by the
suite that exists to certify it.

§3.1 argues the two-field shape ("an honest answer that a fourth field would not improve"). The
argument covers the fourth field; it does not cover gating `Sibling` on the wrong one.

Owed: either gate `Sibling` on its own capability, or state that for a checkpoint store `Persistence`
is defined as "two values over one backing see one row set" and say what `eventmemory` then answers.

**2026-09-08 — the gate moved, and this entry stays open.** The phase-3 plan (`RunCheckpoints`, and
the `Sibling` paragraph under it) settles what the *suite* does: `Sibling` is **required when
`Persistence == Supported`** and its absence is fatal before any section runs — §9.4's own sentence
and §UC-123's first anti-vacuity rule — and the `durability` section is **gated on the capability**,
so `eventmemory.Checkpoints` reports it `not certified` with its own reason while still supplying a
`Sibling` that every section taking a second value uses. An earlier draft made `Sibling` optional and
gating nothing, which deleted the rule for one capability and let an in-memory store report `passed`
beside the word `durability`; that draft is gone. **What this entry asks is untouched**: `Persistence`
and "two values over one backing" are still two claims and `CheckpointCapabilities` still carries
one, so the memory store's cross-value behaviour is certified by no section of its own. Recording
where the gate now lives is not working the entry.

### 10. §UC-104's bounded resolution can conclude "it landed" about somebody else's save  `[medium]`

§3.4's `ErrUncertain` row resolves by loading the checkpoint and comparing the stored advance with the
one this pass tried to write. Under §UC-103's two-replica case the advance it finds may have been
written by the **other** replica, with the other replica's cursor. This process then continues from
its own in-memory cursor and saves at `advance + 1`, which lands, and the loser halts on its next
save — so the fence still bounds it and nothing is skipped (every persisted cursor is some walk's own
resume point). But §UC-104's Then reads as though a matching advance proves *this* save landed, and
§INV-069 says a refused save is "never retried at a re-read advance, which would make two processes
take turns" — which is close enough to what the resolution does that the distinction should be
written rather than inferred.

Owed: §UC-104 states the two-writer interleaving and why it is safe (or adds the discriminator that
makes the conclusion exact), and §INV-069 says why the uncertainty path is not the re-read it forbids.

### 11. `Spec.Wake` has no fan-out or coalescing rule  `[low]`

`Wake <-chan struct{}` is signalled by the application after its own append. A deployment with three
projections over one log that signals one shared channel wakes exactly one of them, at random, per
send — the other two wait out `Idle`. Nothing says whether a wake is coalesced, whether a signal
delivered while a pass is running is remembered, or whether each projection needs its own channel.
§4.2 says a missed wake costs latency and never correctness, which is what keeps this `[low]`.

Owed: one sentence — one channel per projection, and a wake that arrives mid-pass is either
remembered or dropped, say which.

### 12. A panicking or blocking `Observer` is unspecified  `[low]`

`runtime.Supervisor` recovers a panicking observer on purpose (`runtime/supervisor.go:311`,
`observing`). §3.5 publishes "the same value to the `Observer` on every transition" and says nothing
about a panic or a slow implementation, so an observer that blocks stalls the loop and one that panics
takes the runner down — through §3.4's own "a handler panic is recovered" door, which covers `Apply`
and not `Observed`.

Owed: match `runtime`'s answer, or state the opposite deliberately.

### 13. §9.1's `checkPage` row understates the change to a frozen kernel file  `[low]`

The §9.1 table says `event/reader.go`'s "`checkPage` also bounds the returned cursor".
`checkPage(page []Envelope) error` (`event/reader.go:74`) does not receive the cursor, so the entry is
a signature change inside the file the kernel baseline freezes, not an added branch. §11.1's
re-baselining paragraph is about *which* files move; this is about how much of one moves, and the
plan's diff review is where a one-word entry becomes a surprise.

Owed: say `checkPage` takes the cursor as well, or name the new function that checks it.

### 14. Shutdown ordering between closing the store and stopping the supervisor  `[low]`

§3.4 makes `ErrClosed` terminal: a projection that meets a closed store halts and reports unhealthy
through `Ready`. A composition root that closes the event store before it stops the supervisor —
which `fx`'s reverse-order shutdown makes as likely as the other way round — therefore halts every
projection during an ordinary shutdown and reports them unhealthy on the way out. Harmless in
practice, noisy in a log, and indistinguishable from a real halt in whatever the readiness answer
feeds.

Owed: §UC-116 or §3.5 states the expected stop order (supervisor first, then the store), and whether a
halt taken while the context is already cancelled is reported at all.

### 15. `scripts/event_test.go`'s cost comment argues against the row §11.3 adds  `[low]`

The doc comment above `TestNoEventPackageCostsMoreThanTheSeamItNames` ends "which is why `health`,
`port` and `runtime` stay outside it". §11.3 adds `eventExtension + "/projection": "./runtime"`, which
is correct and argued in §3.5 — the comment is what goes stale, and the comment is the place the next
store's author reads before adding a row.

Owed: update the comment in the same change as the row.

---

*Entries §18–§34 were raised by the phase-3 **plan** audit (Round 1,
[`EVENTSOURCE_P3_PLAN_GAPS.md`](EVENTSOURCE_P3_PLAN_GAPS.md)) against
[`EVENTSOURCE_P3_PLAN.md`](../plans/EVENTSOURCE_P3_PLAN.md). §16 and §17 are reserved by the plan
itself and were not taken. The seven blocking findings are in that file and are **not** repeated
here.*

### 18. `pass.go` reads a clock nobody injected, which is the argument D4 used to refuse `Spec.Clock`  `[medium]`

D4 rejects a wall-clock `Tolerate` partly because "`time.Now()` inside the loop … makes this the
first package in the subsystem to read a clock nobody injected — phase 1 made an injected clock a
*store* obligation and this would be the exception". Two paragraphs later `pass.go` builds
`Progress` as `{… At: time.Now()}`, and `State.At` is the same reading. So the exception is taken
anyway, for the two fields a test would most like to pin, and the plan's own argument is spent
where it does not apply and abandoned where it does.

The consequence is small but real: `Progress.At` and `State.At` cannot be asserted in a test, so
the one field of `Progress` that is a timestamp is the one field no case in the plan checks, and
`event/eventpg`'s `updated_at` column — which the plan deliberately binds as `$7` rather than
taking `statement_timestamp()`, precisely so it round-trips — round-trips a value nothing pins.

Owed: either state in D-131 that the projection reads the process clock for observations only and
that this is the disclosed exception to the store-clock rule, or give `Spec` the clock seam and
default it, and pin `Progress.At` in the round-trip case.

### 19. `eventtest`'s `sweep`/`probe`/`admit` cannot be shared "over the factory kind" as a signature change  `[medium]`

The plan says `RunCheckpoints` reuses `sweep`, `probe`, `verdict` and `word` and that this is "three
frozen files changing for **one shared mechanism**", with a stop-and-report trigger "if `sweep`
needs a second body". Reading the code, the trigger fires:

- `admit` (`suite.go:171`) reads `store.Capabilities()` **and `store.Limits()`**, then derives the
  run identity from `limits.MaxKey` and the section list's key budget (`runIdentity`, `reserve`,
  `markOf`, `widestLabel`). A `Checkpoints` has no `Limits`, no key and no stream, so none of that
  body is shared — it is `admit`'s whole body.
- `section.needs` is `func(event.Capabilities, Factory) string`, hard-typed to both.
- `probe` holds `factory Factory` and `opening{capabilities, limits, run}` and every section
  function takes `*probe`.

What is genuinely free is `report.go` — `word`, `verdict`, `verdict.line`, `certified`,
`noVerdictFrom` are already store-agnostic and need **no change at all**, so listing `report.go` as
modified widens the fence for nothing. What is not free is `suite.go` and `probe.go`, and the
manifest fence checks paths rather than the size of a change, so a restructuring of the file that
runs the *store* conformance suite — phase 2's evidence — would be inside the allowed set and
invisible.

Owed: decide before S2 whether `RunCheckpoints` gets its own small runner (reusing `report.go`
only) or `sweep` is genuinely parameterised, and say which; if `suite.go`/`probe.go` are
restructured, the section's report names what changed in them and the full store conformance run is
re-run at both limit configurations as the control.

### 20. `eventpg.Checkpoints` cannot reuse `onExecutor`, `bound`, `opened`, `Migrate` or `Verify` "unchanged" — all five are `*Store` methods  `[medium]`

The plan states that `Checkpoints` "reuses `onExecutor`, `bound`, `opened`, `outcomeOf` and
`causeOf` unchanged" and that `Prepare` "reuses the store's own `Migrate` under `ManageSchema` and
its own `Verify` after". Of those seven, only `outcomeOf` and `causeOf` are free functions
(`classify.go:27,69`). `opened`, `bound` and `onExecutor` are `func (this *Store)`
(`executor.go:33,50,84`); `Migrate` is `func (this *Store)` (`migration.go:154`); `Prepare`,
`Verify`, `Check` and `readMeta` are `func (this *Store)` (`verify.go:27,40,68,94`) and read
`this.db`, `this.schema`, `this.management`, `this.state` and `this.closed`.

So S2 either lifts five methods off `*Store` onto a shared handle — a real refactor of the store,
unnamed in the plan and outside the kernel manifest, therefore invisible to every checkpoint — or
duplicates them, which puts §INV-051 ("opens, commits and rolls back nothing") and §INV-053 ("never
on the pool") in two places that then drift. The plan's own words for that shape are "two accounts
of one rule".

Owed: name the lift (which methods move, onto what) or name the duplication, and in either case
give S2 a case that asserts a `Checkpoints` statement never runs on the pool, rather than
inheriting the assertion from the store's.

### 21. Schema version 2 forces every `eventpg` deployment to migrate, whether or not it ever runs a projection  `[medium]`

`readMeta` (`verify.go:108`) refuses when `version != SchemaVersion`, and level 3 compares the
expected model's tables as a set. Bumping `SchemaVersion` to 2 and putting the fourth table in the
model means an existing v1 deployment that upgrades the library and uses **only** the event store
fails `Verify` at boot until someone runs the v2 list. Under `SchemaManagement: ManageSchema` that
is automatic; under `VerifyOnly` — the deployment that migrates out of band, which is the one that
takes schema changes seriously — the store refuses to start.

That may well be the right answer (one schema, one version, one fingerprint), but it is a
consequence nobody states: neither the plan's D1, nor deliverable 11's `MIGRATIONS.md` row, nor the
module pages say "upgrading to this release requires a migration even if you never build a
`Checkpoints`".

Owed: the `MIGRATIONS.md` v1→v2 row says it in one sentence, and the module page's schema section
says it where an operator reads it.

### 22. `find event -type f` puts gitignored artefacts into the manifest, so `make check` goes red after a coverage run  `[medium]`

The arm enumerates with `find event -type f -not -path 'event/eventpg/*'`. `.gitignore` line 8 is
`*.out` and line 7 is `*.db`; the current arm uses `git status --porcelain`, which does not report
ignored files. So `go test -coverprofile=coverage.out ./...` run from inside `event/`, an editor
scratch file, or any `*.db` left behind makes `check-event-kernel` — and therefore `make check` —
fail with a message about the frozen kernel having moved. A check that goes red for a reason nobody
caused is a check people learn to re-baseline past, which is the failure the arm exists to prevent.

Owed: the enumeration excludes what the repository ignores — either by extension list in the arm,
or by using `git ls-files` when git is available and falling back to `find` with the exclusions when
it is not, with the fallback stated so the two answers cannot differ silently.

### 23. UC-124's restructured `resumption` states two assertions that are not expressible  `[medium]`

The plan restructures `probe.lateWriter` so the late writer's transaction is "held open across the
resumed walk", and says "the section asserts the walk delivers nothing at or beyond the held
position and that the persisted cursor has not passed it".

Neither is expressible as written. `event/store.go:98` says an envelope's position "on an envelope a
read hands back inside the transaction that wrote it and has not committed it … is unspecified: a
store drawing positions from a sequence has one already and a store assigning them at commit answers
zero, and both are conformant" — so the suite cannot name the held position before the commit. And
"the persisted cursor has not passed it" would need a reading of a cursor as a position, which
§INV-066 and §1.4 forbid and which no API offers.

The third assertion the plan names — commit, then resume from that cursor and require the held event
to be delivered — is expressible and is the one that falsifies a `max(position)` cursor, so the
section is achievable; the first two need restating (assert post-hoc, after the commit, that nothing
delivered pre-commit had a position at or above the held one; drop the cursor comparison).

Owed: the plan states the three assertions in an order and a vocabulary the store contract admits,
before S2 writes them.

### 24. S1's checkpoint runs the real-repository kernel test before regenerating the manifest  `[medium]`

S1's arms run, in order: `go test … -run '^(TestCheckEventKernelReportsADifferenceAndOtherwiseOk|
TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt)$' ./scripts/`, then
`./scripts/checks.sh event-kernel-baseline`, then `./scripts/checks.sh event-kernel`. The second
test in that `-run` runs the real arm against the real repository, which at that point is still
comparing against the pre-S1 manifest — S1 has just added `event/checkpoint.go` and modified three
kernel files. It fails.

Owed: regenerate the baseline before the arm that reads it, in S1 and in every section that copies
the shape.

### 25. Renaming the kernel arm's self-test in S1 leaves `TestEveryTestNameTheDocsCiteExists` red until S5  `[medium]`

S1 replaces `TestTheEventKernelOfThisRepositoryIsWhereThePhaseOneCommitLeftIt` with
`TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt`. That name is cited by
`docs/ai/flows/FL-037-a-recorded-fact-becomes-a-postgresql-row.md:309`, and
`scripts/docs_test.go:49 TestEveryTestNameTheDocsCiteExists` reads every backticked test name in
`docs/` and fails on one no `_test.go` declares. S1's checkpoint does not run it and S2's and S3's
run only `./event/...`, so the tree carries a failing `make unit` from S1 to S5 — against the plan's
own ordering rule, "`make unit` stays green throughout".

Owed: FL-037's citation moves in the same section as the rename, which is what CLAUDE.md's
same-change rule already requires.

### 26. `event/projection` inside the frozen-kernel manifest changes what the arm means, and the plan does not say so  `[medium]`

The manifest's subject is everything under `event/` outside `event/eventpg`, and phase 3 puts a
nine-file consumer package plus its tests inside it. From phase 4 onward every projection bug fix,
every added test and every doc comment in `event/projection` is a "the frozen kernel moved" failure
requiring a re-baseline — for a package that is a *consumer* of the kernel rather than part of the
vocabulary two stores implement. The arm's signal-to-noise is what makes it survivable.

Owed: either the manifest's subject excludes `event/projection` (with a sentence saying the kernel
is the vocabulary and the suite, not every package that happens to live under `event/`), or the plan
states that the arm now covers the consumer too and why that is wanted.

### 27. What a store failure on `Save` retries — the save alone or the whole pass — is still unstated in the plan  `[medium]`

`## P3` §6 raised this against the spec; the plan does not decide it. `pass.go`'s table says
"`ErrBackend` | read or save | retryable, without limit", which under `InUnit` must mean the whole
unit (the unit rolled back and the handler's writes with it) and under `AfterApply` could mean
either the save alone or the whole pass — a difference of one duplicate delivery per store hiccup
against a handler that was promised at-least-once and may be expensive.

Owed: the plan says which, per mode, and the retry case asserts the handler call count over a
failing save.

### 28. The quarantine sink is handed an envelope the handler may legally have rewritten, and may record it twice  `[medium]`

D5 decides *where* the sink is called and not *what it is handed*. Two things follow that nothing
states:

- the isolation pass hands the handler a copy per attempt (§3.3) and then hands the sink an
  envelope; if that is the same value the handler was given, a handler that wrote into its page —
  legal under §INV-021's grant, and the very thing `TestARetryReApplies…`'s control exercises —
  makes the quarantine record name mutated bytes.
- under `AfterApply` the sink is called outside every unit, so a crash between the sink write and
  the advance re-delivers the page and the isolation pass records the same envelope a second time.
  D5 names double-recording as the argument against calling the sink outside the unit under
  `InUnit`, and then leaves the same duplicate unmentioned for the mode that has no unit at all.

Owed: the module page's `Quarantines` contract row says the sink is handed its own copy, and that
under `AfterApply` a sink is at-least-once and owes the same idempotency a handler does.

### 29. `Progress.Applied` and `Progress.Quarantined` count redeliveries and isolation re-applies twice  `[medium]`

The plan defines both as cumulative envelope counts seeded from the first `Load`, which closes half
of `## P3` §5. What it does not say is that a redelivered page's envelopes are counted again, and
that an envelope which applied inside a failed attempt and again inside the isolation pass is
counted twice. So `Applied` is "envelope applications", not "events in the read model", and the
difference is exactly the number a dashboard would read it as.

Owed: one sentence in the module page and in the column comment.

### 30. `projection.Phase`, `Observer`, `ObserverFunc` and `State` shadow four `runtime` names in a package that imports `runtime`  `[low]`

`runtime` already exports `Phase` (idle/running/stopped/failed), `Observer`, `ObserverFunc` and
`RunnerState`, and a composition root that wires a supervised projection holds both in one file.
The projection's phases are genuinely different values and the duplication is probably right, but
the plan never says so, and the one place it would be read — D-129 — does not mention it.

Owed: a sentence in D-129 saying why the four are re-spelled rather than reused, in the same place
the `jobs` re-spellings are argued.

### 31. `*Tracker` is exported with no stated concurrency rule, where `*Reader` states one  `[low]`

`event/reader.go:41` says of `*Reader`: "The one stateful value in the caller-facing surface, and it
is for one goroutine at a time". `*Tracker` holds `advance` and `loaded` with no synchronisation and
is exported precisely so a second consumer can have one, and the plan's contract says nothing about
how many goroutines may hold it.

Owed: the same sentence `Reader` carries, on `Track`'s doc or the module page's checkpoint row.

### 32. The second data source §UC-128 and S4 need is never provisioned  `[low]`

S4's `TestThreeWiringsToASecondDatabaseAreToldApart` needs "a second live database" and the gate
supplies exactly one DSN (`FROSTGROVE_EVENTPG_TEST_DSN`), whose unset case must fail rather than
skip. The plan does not say what the second source is — a second `*sql.DB` over the same DSN (a
different pool, therefore a different transaction, which is all the case actually measures), a
second schema, or a second database that has to be created. The wrong choice — the same
`crud.Source` — makes the case fail rather than pass silently, so this is low, but it is an hour
somebody spends in the middle of the live section.

Owed: S4 says which, in one clause, and whether it needs `CREATE DATABASE` on the gate host.

### 33. `event/bounds.go`'s own comment says "Five bound the factors of a product" and phase 3 adds a sixth  `[low]`

`MaxCursorBytes` joins `MaxPayloadBytes`, `MaxNameBytes`, `MaxKeyBytes`, `MaxBatchCount` and
`MaxPageCount`, making the count in the file's header comment wrong — and the new constant is not a
factor of the resident-bytes product the sentence is about, which is worth one clause.

Owed: the comment moves in the same change as the constant.

### 34. An over-ceiling cursor is refused with `ErrTooLarge`, which wraps `crud.ErrBadRequest`  `[low]`

`Tracker.Save` refuses a cursor over `MaxCursorBytes` with `ErrTooLarge`
(`event/errors.go:45`: `fmt.Errorf("…: %w", crud.ErrBadRequest)`), so a **store** minting an
oversized cursor renders through a transport as a client's 413. Every other store-honesty refusal in
the kernel is `ErrBackend`, which is what `Reader.checkPage`'s new arm uses for the identical
defect one door over.

Owed: decide whether the two doors classify the same store defect the same way, and say why if they
do not.

### 35. [SPEC] §3.1's DDL table is the last place `cursor text` and an unbounded-below cursor survive  `[low]`

The phase-3 plan overrides both, with measurements: the column is `bytea`, because `event.Cursor` is
unconstrained bytes and PostgreSQL `text` refuses a NUL and invalid UTF-8 (D11, and `events.payload`
already answers the identical question one table over); and it carries `octet_length(cursor) >= 1`,
because the empty cursor is the origin of the log in both shipped stores and a stored empty cursor at
a non-zero advance restarts a projection against a live read model with no error on any path (D12).
The frozen semantics are not edited, so §3.1's table now says something the shipped schema does not.

Owed: phase 4 either amends the spec's table or records that the plan's D11/D12 supersede it, so the
next reader of §3.1 does not write `text`.

### 36. `Track` runs one of the four store-honesty checks the log's own door runs  `[medium]`

`admit(log)` (`event/binding.go:66-81`) refuses a store that is nil, one whose `Backing()` is
invalid — "this store does not say what it writes to" — and one whose `Capabilities()` leaves any
`Support` `Unstated`, on the stated ground that "a capability nobody stated is not one nobody has".
`Track` (`event/checkpoint.go:76-84`) refuses only the nil, and §3.1 says a checkpoint store "is a
second store interface with an invitation for third-party implementations, so it needs the same door
rather than the same sentence". A third-party `Checkpoints` returning `CheckpointCapabilities{}` and
`Backing{}` is admitted; `Persistence: Unstated` then reads to an operator as nothing at all, which
is exactly what §UC-100 needs `Persistence` for. It fails in the safe direction —
`Backing{}.Equal(Backing{})` is false because `crud.SameDataSource(nil, nil)` is false, so no
destination comparison passes spuriously — which is what keeps this `[medium]`.

Owed: either `Track` calls the two predicates that already exist (`Backing.valid`,
`Support.stated`), or §3.1's "same door" sentence is narrowed to the two checks it actually means.

### 37. `Reader.checkPage` bounds the page count and the cursor but never the payload bytes  `[medium]`

`Fact.Read`'s comment (`event/fact.go:60-63`) says the kernel's `MaxPayloadBytes` is "the second of
two rather than the only one", because "the store's own read door already refuses a row over its own
bound". On the **global** read path there is no such first bound in the kernel:
`Reader.checkPage` (`event/reader.go:84-99`) holds `this.limits` and checks only `MaxRead`, the
cursor ceiling and ascent. The stream path does bound it — `Repo.apply` refuses
`len(envelope.Payload) > this.limits.MaxPayload` (`event/repo.go:266`) — so the two read doors
disagree. A store declaring `MaxPayload: 1024` and answering a page of 256 one-megabyte payloads is
refused by neither: `MaxResidentBytes` is only enforced at bind time, derived through
`ResidentPage(MaxPayload)` on the assumption the store honours its own number.

Owed: decide whether `checkPage` applies `this.limits.MaxPayload` per envelope the way `Repo.apply`
does, and correct `Fact.Read`'s "second of two" if it does not. Pre-dates phase 3; S1 is where the
claim was written down.

### 38. A store-classified checkpoint failure reaches the caller naming no projection  `[medium]`

Every refusal the door raises itself carries `%q` of the name (`event/checkpoint.go:146-179`).
Every refusal that comes back **through** the store does not: `Tracker.Save` and `Tracker.Forget`
return `refuseAppend(err)` and `Tracker.Load` returns `refuseRead(err)` verbatim, so a fenced loser
reads `event: the stream is not at the version this append was decided at: conflict` — measured —
with no name, no advance and a sentence about streams and appends. An operator running twelve
projections against one checkpoint store cannot tell which one stopped. §INV-076 forbids a new
sentinel and does not forbid context.

Owed: decide whether the three pass-throughs may add the projection name without disturbing the
refusal-rendering rules, and record the answer.

### 39. The door's over-ceiling arm is asserted through `resumeFrom` and is confounded  `[medium]`

`TestTheDoorRefusesAnAnswerAboutAnotherName`'s third row drives `Load → Read → Next` and asserts
`errors.Is(err, ErrWrongStore)`. Removing `admit`'s `len(held.Cursor) > MaxCursorBytes` arm still
fails the case — measured — but through the fixture log's own refusal, `event: this cursor was not
minted over this backing, or it is no longer readable`, not through the door. §INV-078's
falsification asks for "a `Checkpoints` whose `Load` answers one, asserting `Tracker.Load` refuses
it as `ErrWrongStore`", which is an assertion on `Load` alone.

Owed: assert the ceiling row against `Tracker.Load` directly, and keep the `resumeFrom` walk for the
rows about a walk.

### 40. S1's eleven new exported symbols are in no module page and in no surface baseline  `[medium]`

`docs/modules/en/event.md` and `docs/modules/ru/event.md` name none of `Checkpoint`, `Progress`,
`CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker`, `MaxCursorBytes`, `Fact.Family` or
`Fact.Read`; `grep -c` over both is 0. `docs/api/surface.md` likewise. The plan schedules all of it
in S5 and `make api` is deliberately outside `make check`, so nothing is red — but CLAUDE.md's rule
for a public API is "update in the same change as the code, never later", and until S5 lands, a
consumer's reference describes a package that no longer exists.

Owed: S5 regenerates the surface and writes both module pages, and a note in the plan records that
S1–S4 deliberately run with the module page stale.

### 41. The empty-cursor origin rule is a bare `== ""` in five places and has no name  `[low]`

D12 makes "the empty cursor IS the origin of the log" the load-bearing fact of three kernel doors,
and it is spelled as an unnamed comparison at `event/reader.go:91`, `event/checkpoint.go:145`,
`event/checkpoint.go:151` and `event/checkpoint.go:174`, plus `eventpg/cursor.go:54` and
`eventmemory`'s own reader — each with its own paragraph restating the same sentence. `Cursor` is
exported and a third-party store has to know the convention from prose.

Owed: consider one named predicate on `Cursor` that the doors and the stores share, so the rule is
declared once and the comments shrink to one.

### 42. `Fact.Read` is the one reading door on a declaration that does not seal the aggregate  `[low]`

`Fact.New` and `Fact.RoundTrip` call `this.aggregate.seal()`, and `Fact.Family` seals through
`Aggregate.Family`. `Fact.Read` reads `this.aggregate.family` directly. It is safe today — `family`
is immutable after `Define` and `Read` consults only this fact's own chain, never
`aggregate.facts` — so a `Declare` racing a `Read` cannot change what `Read` answers. What it does
mean is that a projection can decode a whole log without ever sealing the aggregate it decodes
through, and a late `Declare` then succeeds behind a router that sealed on its first `Apply`.

Owed: decide whether the projection's decode door seals, and say which of the two rules `Fact.Read`
follows.

### 43. `event-kernel-moved` reads an empty allowed set as "allow everything"  `[low]`

`[[ $path =~ $allowed ]]` with `allowed` empty matches every path, so
`./scripts/checks.sh event-kernel-moved <pred> ''` reports ok for any move; the arity guard is
`(( $# < 2 ))` and an empty second argument satisfies it. Separately, a required path is matched
with `[[ $'\n'$moved$'\n' == *$'\n'$path$'\n'* ]]`, where `$path` is unquoted on the right of `==`
and is therefore a glob — a required path containing `*` or `?` would match loosely. Neither is
reachable from the checkpoints the plan writes.

Owed: refuse an empty allowed set by name, and quote the required path in the membership test.

### 44. S1's fence cannot be re-run outside the clone that recorded it  `[low]`

The predecessor lives at `.git/event_kernel_before_s1`, which the plan calls scratch tied to this
clone. A reviewer on a fresh checkout cannot run S1's own `event-kernel-moved` arm at all. The
reconstruction is exact and cheap and was used for this review — every one of the 104 recorded lines
equals `git show HEAD:<path> | sha256sum`, with `git ls-tree -r --name-only HEAD event | grep -v
'^event/eventpg/'` giving the same 104 files — but it is nowhere written down.

Owed: record the reconstruction command beside the fence in the plan, so a section's evidence
survives the clone that produced it.

### 45. The `absence` section compares `Progress` with `==`, which the kernel refuses to do three files away  `[medium]`

`event/eventtest/sections_checkpoints.go:91` reads `case absent.Progress != (event.Progress{}):`.
`event.Progress` carries a `time.Time`, and `event/checkpoint.go:29-34` exists solely to say why
that comparison must not be written: "Field by field and never with `==`, because `time.Time`'s
equality carries a monotonic reading and a `*Location`: a store answering a zero instant in another
location would be refused for a difference that is not one." `Tracker.admit` calls
`Progress.zero()`; the suite, which is what a third-party store is measured by, does the thing the
door refuses to do. `event/eventtest/checkpoints.go:260-268` (`sameCheckpoint`) already carries the
correct field-by-field form three lines up, so this is one call away from being right.

A store answering `time.Time{}.In(someLocation)` for an absent row — a plausible scan of a NULL
timestamp through a driver that attaches a session zone — is reported broken for a difference the
kernel says is not one. Neither shipped store trips it, which is why this is `[medium]`.

Owed: `!absent.Progress.zero()`-equivalent in the section (a `zeroProgress` helper beside
`sameCheckpoint`, since `zero()` is unexported in `event`), and the same treatment for
`event/eventmemory/checkpoints_test.go:103` and `:134` and
`event/eventmemory/transaction_test.go:625`, which compare whole `event.Checkpoint` values with
`!=` for the same reason.

### 46. Four checkpoint sections blame the wrong field when a row differs in `Progress`  `[medium]`

Every section that reads a row back funnels through `sameCheckpoint`, and each then prints a message
about the one thing that section is *named* for rather than the field that actually differed.
Measured, against a store whose only difference is the grain of its instant column (the `[high]`
GAP-2 of [`EVENTSOURCE_P3_S2_GAPS.md`](EVENTSOURCE_P3_S2_GAPS.md)):

- `event/eventtest/sections_checkpoints.go:243-246` — "a cursor of exactly the 4096 bytes the
  kernel publishes as the ceiling was saved and answered back as 4096 bytes, byte 4096 first
  differing". The cursor is byte-identical; `firstDifference` returns `min(len, len)` when nothing
  differs, so the index it prints is the length.
- `event/eventtest/sections_checkpoints.go:390-394` — "one saver alone was refused after the
  contended round, so this fence refuses everything rather than all but one". The saver was not
  refused; `this.save` would have failed the section if it had been.
- `lifecycle` and `durability` print `unchangedRow`'s "%+v where it held %+v", which is honest but
  buries the one differing field in two seven-field renderings.

CLAUDE.md's rule is "the failure message states what broke in plain words". Two of these state
something false, which is worse than `got != want`.

Owed: `sameCheckpoint` answers *what* differed (a field name, or a small diff value) and the four
call sites print it; `firstDifference` answers -1 when nothing differs and the cursor arm only fires
when the cursor is what moved.

### 47. `Forget`'s projection-name refusals diverge between the two shipped stores, and no section asks  `[medium]`

`event/eventmemory/checkpoints.go:138` calls `refusable(projection)` from `Forget`, so a name over
`event.MaxNameBytes` answers `event.Failure(event.Refused, …)`. `event/eventpg/checkpoints.go:312`
checks only `projection == ""`, so the same call answers **nil** — the `DELETE` matches nothing and
the store reports a successful retirement of a name it cannot key a row by. `Save` agrees in both
stores (S2's departure 7); `Forget` does not.

No `RunCheckpoints` section asks about the projection-name bound at all — `bounds` is the cursor's
two limits only — so neither the divergence nor a third store that omits the check is visible. The
door (`event.Track`, `checkName`) refuses an illegal name at construction, so a `*Tracker` can never
present one; that is why this is `[medium]` rather than blocking. The suite runs against the raw
`Checkpoints` precisely so an implementer is told what the door covers for them, and here it is not.

Owed: one refusal shape for `Forget` in both stores, and a `bounds` arm that presents an unnamed and
an over-long projection to `Save` and `Forget` and pins the class.

### 48. S2's six new exported symbols are in no module page, in either language  `[medium]`

`eventmemory.CheckpointSpec`/`Checkpoints`/`NewCheckpoints`, `eventpg.CheckpointSpec`/
`Checkpoints`/`NewCheckpoints`, and `eventtest.CheckpointFactory`/`RunCheckpoints` are on the
regenerated `docs/api/surface.md` and in `docs/ai/flows/Index.md`, and `grep -c 'NewCheckpoints\|
RunCheckpoints'` over `docs/modules/en/{event,eventmemory,eventtest,eventpg}.md` and the `ru/` half
answers **0** for all eight files. The `eventpg` page's only S2 edit is "eleven statements" →
"thirteen".

The plan schedules the module pages in S5, so this is the plan working rather than a section
skipping its docs — it is recorded here because §40 already records the same debt for S1's eleven
symbols and the two must close together, and because CLAUDE.md's table makes a module page a
same-change obligation for a new public API.

Owed: fold into §40 — one closing act in S5 covering S1's and S2's exported surface in both
languages, with the `Checkpoints` contract row a third-party implementer reads (what `Save` admits,
what `Load` must answer, what `Forget` is not fenced against, and what `Transaction` says).

### 49. `event/eventtest/checkpoints_test.go:184` holds process-wide mutable state in a suite whose own inventory forbids it  `[low]`

`var minted atomic.Uint64` is package-level and shared by every `checkpointFactory` the file builds,
including the ones the mutation harness runs in sequence. `event/eventtest/inventory.go:5-9` states
the rule the file next door lives by — "one slice, built here so nothing is package-level and
mutable". Nothing is wrong today (the counter only has to be strictly increasing), but the fixture
is the one a third party copies.

Owed: move the counter onto the factory's own closure, as `pgMinter` and `minter` already do.

### 50. `eventpg` classifies an advance above `bigint` as `Conflict`, which tells a consumer the row moved  `[low]`

`event/eventpg/checkpoints.go:246-248` answers `event.Failure(event.Conflict, errAdvanceAhead)` for
`checkpoint.Advance > math.MaxInt64`. `Conflict` is the class that means "another writer moved the
row", and `pass.go` will treat it as a second replica having won; the actual condition is this
store declining to write a number its own column cannot hold, which is `Refused` — the class the
same function uses for every other bound in `refusable`. Unreachable in practice (2^63 saves), which
is why it is `[low]`; it is the classification a reader copies.

Owed: `Refused`, beside the other four bound refusals, and the message says the column rather than
the row.

### 51. The checkpoint suite reads an outcome off `err.Error()` rather than off the value  `[low]`

`event/eventtest/checkpoints.go:241-248` (`classified`), `sections_checkpoints.go:315` (the
cancellation arm) and `:376` (the concurrency arm) compare `err.Error()` against
`event.Failure(outcome, nil).Error()` and `context.Canceled.Error()`. CLAUDE.md: "Compare errors
with `errors.Is` against the exported sentinels, never by string." The intent is defensible — a
`failure` deliberately does not unwrap, and the arms want the *bare* cancellation rather than any
wrapping of it — and it is the store suite's existing idiom
(`event/eventtest/sections_lifecycle.go:185,331`), so this is `[low]` and is about the whole package
rather than S2. But it means a store whose failure text drifts by one character is certified, and a
store that wraps a cancellation is refused for a reason the message does not give.

Owed: an exported predicate on the kernel side — `event.Classified(err) (Outcome, bool)` or
equivalent — so a suite, a decorator and a consumer can all read the outcome off the value; then the
three arms use it and the cancellation arm says `err == context.Canceled` in as many words.

### 52. The isolation pass does not claim the checkpoint row before it applies  `[low]` — **raised by S3**

[[D-133]] presents the advance before the handler under `InUnit`, so a fenced save taken under the
row's own lock refuses a second live instance before it applies anything. The **isolation pass** is
the one applier that still saves last, because what it quarantined is what its own run discovered
and a claim carrying a count of what the sink took before the sink was called would be a number
nobody measured. So a losing instance that is inside an isolation pass — which it only reaches after
its own handler failed permanently on the whole page — applies and quarantines, and then rolls back
with the refused advance. Nothing is lost and nothing is double-committed; what is paid is the work,
and a sink that writes outside the unit records an envelope that is redelivered.

The shape that would close it is a two-number tally the pass can state up front — "at most this many
envelopes, at most this many quarantined" — which is a wider `Progress` contract than [SPEC] §9.4
fixes. Not worth it for the rare path; recorded so the asymmetry is not read as an oversight.

### 53. Two live instances under `AfterApply` both apply the overlapping page, and no mechanism prevents it  `[low]` — **raised by S3, doc owed in S5**

Outside a unit of work there is no lock to hold across a handler call and nothing this framework may
open ([[D-126]]), so the reference implementation's claim-before-read has no `AfterApply` analogue:
both instances read the same page, both call `Handler.Apply`, and only then does the fence pick a
winner. Delivery is at least once and the handler already owes idempotency, so this is not a
correctness defect — it is an **undocumented** one, and [[D-133]] plus §Adopt A2 of the reference
adjudication put the row it needs on the module page: under `AfterApply`, `Placement: Singleton` is a
promise to the deployment rather than an enforcement, and the overlapping page lands twice.

### 54. `Progress.Highest` and the stored cursor can move backwards under contention  `[medium]` — **raised by the S3 review · RESOLVED by S4's review, 2026-09-09**

> **Resolved, and the analysis below was wrong about the severity.** *"Nothing is lost"* is false:
> A does **not** resume from the lower cursor, it resumes from its own **higher** one, so every
> position between the two is applied by nobody and is behind the checkpoint at A's next save.
> Driven live on 17.9 as S4's GAP-1 (`[critical]`) — the read model ended holding `[s-1 s-4]` over
> a log of four with the row at advance 2, highest 4, quarantined 0. The last sentence's second
> option is what shipped: the settlement now compares the **cursor**, and a row at the presented
> advance carrying one this pass never presented is `overtaken` — the row adopted, the reader
> rebuilt from its cursor. See `EVENTSOURCE_P3_S4_GAPS.md` GAP-1, [[D-133]]'s table and
> `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt`. The entry is kept as it was written
> because the mistake in it is the finding: a resume point read as "at least once" was a skip.

`Projection.confirmed` (`event/projection/pass.go:411-418`) is entered when the settling load finds
the row at exactly the advance this pass presented and the save was merely *unconfirmed*. The table
reads that as "this pass wrote it", which is right whenever there is one writer. Under two live
instances it is ambiguous: instance B, at the same fence, may have written that advance while A's own
save never landed. A then continues from **its own** in-memory cursor and its next save replaces B's
row — including B's `Progress.Highest` — with A's, which is lower whenever settlement let B read
further than A did.

Nothing is lost: every position at or below either `Highest` was delivered to some handler, and a
resume from the lower cursor redelivers rather than skips, which is at least once. What moves is a
number an operator reads as lag and §6 reads as a cutover signal, and [[D-128]] states `Highest` as a
watermark without saying it is non-monotone while two instances contend. Owed: one sentence beside
`Progress.Highest`, or the resolution taking the row's cursor as `overtaken` does.

### 55. D5's *the quarantine sink is called inside the unit* is pinned by no test  `[medium]` — **raised by the S3 review**

The plan's D5 decides that a `Quarantines` sink runs **inside** the unit under `InUnit`, because a
sink called outside it is the one write that survives the rollback of the advance, and the module page
owes an implementer that contract row. `Projection.oneAtATime` (`event/projection/pass.go:232-254`) is
reached through `claimed` and therefore does run inside the unit — but every quarantine case in
`event/projection/retry_test.go:262-359` leaves `Spec.Advance` at its default, so all four run under
`AfterApply`, and S4's `TestAQuarantineIsEnvelopeGranular` does not name a mode either. The decision
is implemented and unproven. Owed: one case that quarantines under `InUnit` and asserts the sink was
handed a context the unit bound, and one that asserts a sink writing through that context is discarded
when the unit rolls back.

**Half closed by S4, 2026-09-09.** `TestAQuarantineIsEnvelopeGranular`
(`event/eventpg/projection_integration_test.go`) now runs under `InUnit` against a live database — it
had to, because that is the only mode in which the isolation pass's row count is exact — and its sink
asserts it was handed a context carrying a transaction of the checkpoint store's own source, failing
with *"the sink was called on a context carrying no transaction … so its record does not commit with
the advance it accounts for"*. **Still owed:** the rollback half — a sink that wrote through that
context and a unit that then fails, asserting the record went back with the advance. Left alone under
the policy.

### 56. Three arms of the settlement table are unreachable from any case  `[medium]` — **raised by the S3 review**

`Projection.settle` (`event/projection/pass.go:368-393`) has six arms and S3's cases drive three:
`found.Advance == presented && fenced` (both two-instance cases), `found.Advance+1 == presented &&
InUnit` (`TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory`) and the default
(`TestAForgottenCheckpointRefusesTheNextSaveAndHalts`). Unreached anywhere in S3, and named by no test
in S4's list either: `found.Advance > presented` (several passes of another instance already past
this one), `found.Advance == presented` unconfirmed (`confirmed`, which S4's
`TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` covers only for the single-writer reading)
and `found.Advance+1 == presented && fenced` (`errFenceRefused`), whose only reachable trigger today
is GAP-1's mis-derived advance. A six-way table with three untried arms is three untested halts on the
path a deployment reaches only when something has already gone wrong.

### 57. The isolation pass is told apart by `apply.foreseen == nil`  `[low]` — **raised by the S3 review**

`Projection.claimed` (`event/projection/pass.go:192-212`) decides whether to claim the advance before
or after the handler by asking whether the applier carries a `foreseen` closure. The predicate is
true, the comment above it explains why, and the name of the thing being asked — *is this an applier
whose tally only its own run knows* — appears nowhere in the expression. A named method on `applier`
would put the question where the answer is.

### 58. The idle ticker is created once for the loop's life  `[low]` — **raised by the S3 review**

`Projection.Run` (`event/projection/projection.go:112`) builds one `spec.Ticks(spec.Idle)` and
`follow` waits on it for every poll. `runtime.SystemTicks` is a `time.Ticker`
(`runtime/periodic.go:19`), so the wait is fixed-rate with a dropped tick rather than the fixed delay
technique R27 of the reference adjudication records as vv's property (*"`follow` waits after the pass
ends — `fixedDelay` semantics"*). The observable difference is one immediate poll after any pass that
outlasted `Idle`; harmless, and recorded so the claimed property and the code agree or the claim is
corrected.

### 59. [[D-128]]'s **Proven by** cites the wrong line range  `[low]` — **raised by the S3 review**

`docs/ai/decisions/D-128-the-log-delivers-in-position-order.md:196` cites
`TestAConsumerReadsThroughPagesTheStorePublished (event/reader_test.go:69-97)`; the test begins at
`event/reader_test.go:14` and the cited range is the body of one of its cases.

### 60. `checkUnit`'s two `Destination` arms cover one another  `[low]` — **raised by S4's mutation pass**

`event/projection/pass.go:547-553` refuses a `Destination` the unit bound no executor for and then
refuses one whose executor is not a transaction. Removing only the first arm changes nothing an
observer can see: `crud.IsTransaction(nil)` is false, so the second arm refuses the unbound case too,
with a message about a transaction rather than about a binding. The whole `Destination` resolution has
to be removed before §UC-107(e) fails — measured during S4's mutation pass, which is why the recorded
mutation removes the resolution rather than the arm. Two refusals, one reachable defect, and the
message an operator reads for a Destination naming another database is the wrong one of the two.
Owed: either one arm, or a case that pins each message to its own wiring.

### 61. `stopping()` reads a handler's own `context.DeadlineExceeded` as a shutdown  `[medium]` — **raised by the S4 review**

`event/projection/pass.go:571-573` classifies any error carrying `context.Canceled` or
`context.DeadlineExceeded` as the loop stopping, and `applyFailed`/`saveFailed`/`refused` return it
straight out of `Run`. The value is read rather than the context: a handler that calls an HTTP
client, a second database or a queue with a deadline of its own and returns that failure takes the
whole runner down — `runtime/supervisor.go:165` accepts only `context.Canceled` as an expected
return, so a `DeadlineExceeded` from a handler is reported as a failed runner and by default takes
the process with it. The robust form asks `ctx.Err() != nil` and treats the error value as a
handler failure like any other. Not raised as blocking because the shape is S3's and no case in
the suite reaches it; owed either as a change or as a sentence on the module page telling a
handler never to return its own deadline error.

### 62. S4's report claims the row counts are read on a third pool, and they are read on the store's  `[low]` — **raised by the S4 review**

The plan's S4 section says *"the row counts are read out of the database over a pool that is
neither the projection's nor the handler's"*. `destination.read`
(`event/eventpg/projectioncase_integration_test.go:115-134`) reads over `projectionCase.pool`,
which is the pool the `Store` was built on (`:43`) and the pool the handler writes through
(`:69`). The **checkpoint row** is read on a third pool (`maybeStoredCheckpoint` → `liveDB`), and
the read model is cross-checked against `psql`, so the independence the sentence is about is
really there by another route — but the sentence as written is not what the code does. Fix the
sentence or the pool.

### 63. A lost fence reaches the `Observer` only on the first turn  `[low]` — **raised by the S4 review**

`event/projection/state.go:53-66` publishes only when the phase, the attempt or the nil-ness of
the error changes. `overtaken` (`pass.go:462-475`) sets `attempt = 0` and transitions to
`PhaseRetrying` with `ErrOvertaken` every time, so the second and every later lost fence in one
streak publish nothing at all: an operator watching the observer sees one `ErrOvertaken` and then
silence while the contention continues. `Ready` still answers once the streak outlasts
`Tolerate`, which is why this is `[low]` rather than a hole; but the module page should not claim
the observer carries the contention.

### 64. [[D-128]] cites the wrong line for `Progress.Highest`'s filling  `[low]` — **raised by the S4 review**

`docs/ai/decisions/D-128-the-log-delivers-in-position-order.md:129` says *"`event/projection/pass.go:220`
fills it as `this.page[len(this.page)-1].Position`"*. Line 220 is inside `claimed`; the fill is in
`presentSave`, `event/projection/pass.go:295-305`. Same class as §59.

### 65. The three surface walks read `docs/api/surface.md` and nothing keeps it current  `[medium]` — **raised by S5**

`scripts/projection_test.go`'s `checkedEventPackages` takes its package list from the `##` headings
of `docs/api/surface.md`, which is the right source — that file is what a release reads, so a
package missing from it is a package nothing walks. But **nothing regenerates it before the walks
run.** `make api` is a separate command, `make check` does not call it, and `make unit` runs the
walks against whatever is committed.

The list is fail-closed in two places only: fewer than five event packages, or `event/projection`
missing, fails the run. A **sixth** package added under `event/` without a `make api` is therefore
walked by none of `TestNoExportedFunctionTakesAPositionAndAnswersACursor`,
`TestNoConstructorTakesAProgressAndAnswersACursor`, `TestCursorIsNeverCompared` or
`TestNoSnapshotAuthorityIsDeclaredOrPromised`, silently, and the `checked`/`walked` floors do not
notice because the five that are listed carry them.

S5's checkpoint runs `make api` **before** the count and the walks for exactly this reason, so the
section's own evidence is of the right artefact. What is owed is a mechanism rather than an order in
one command: either a `check-api-current` arm that regenerates into a temporary file and diffs, or
deriving the package list from `go list ./event/...` and asserting it equals the baseline's, which
turns a stale baseline into a red line rather than a narrower walk. The first is what `make check`
should hold; the second is what makes the walk itself honest.

### 66. The comment walk narrows the docs walker's window by one clause, so one invariant has two models  `[low]` — **raised by S5**

`TestNoDocPromisesExactlyOnceDelivery` decides whether a phrase is *about delivery* from
`block.around(offset)` — the sentence before the claim plus the whole sentence the claim sits in.
`TestNoCommentInTheProjectionPackagePromisesExactlyOnce` uses `block.attachedTo(at)` instead — the
sentence before plus the claim's own **clause** — because a Go comment packs several subjects into
one paragraph and the wider window reports `event/projection/doc.go`'s *"Spec.Unit runs the work it
is given exactly once, and a unit that retries its transaction answers the failure instead: the
page is re-delivered rather than saved over twice"*, which is a statement about a unit of work and
not about delivery.

The narrowing is deliberate and stated in the test, and it costs sensitivity in exactly one shape: a
comment whose delivery vocabulary appears **only after** the claim's clause — *"delivered exactly
once, so the projection needs no idempotent handler"* is caught (the clause carries "delivered"),
but *"applied exactly once; the handler therefore needs no inbox"* is not. §INV-072 now has two
readers with two windows, which is the shape that drifts. Owed: one model with one window, or a
recorded statement of which shapes each is blind to, in `[[FL-036]]` beside the residual that file
already records for the negation model.

### 67. The worked second-database example demonstrates two pools rather than two servers  `[low]` — **raised by S5**

`_examples/event-checkpoints-elsewhere` takes `-checkpoints` and `-read-model` DSNs and **defaults
both to the same one**, so `GOWORK=off go run ./event-checkpoints-elsewhere` proves the wiring over
two `*sql.DB` handles of one server. That is the property `crud` actually measures — `SameDataSource`
compares the handle, so two pools are two data sources and the unit binds a transaction for exactly
one of them — and it is what makes `Destination: projection.Unchecked` necessary. It is *not* the
cross-server case a reader may take it for, and the difference matters for a reader deciding whether
their own read model is "elsewhere". Owed: a sentence in the example's own doc comment saying which
of the two it runs by default, or a second default that points somewhere else.

### 68. The five walks type-check every package of the extension from source on every `make unit`  `[low]` — **raised by S5**

`scripts/projection_test.go` builds one `importer.ForCompiler(fset, "source", nil)` and type-checks
`event`, `event/eventmemory`, `event/eventtest`, `event/projection` and `event/eventpg` from
source — measured at ~1.2 s of the `scripts` package's ~7 s. The packages are cached across the
five walks and the importer is shared, so the cost is paid once rather than five times, and it buys
what a syntactic walk cannot have: `Cursor`, `event.Cursor` and a local alias of either are one type
and three names. Recorded beside `## P2` §32 and §46, which are the same accounting for the
`event/eventpg` binaries, so whoever decides the suite is too slow decides it over the whole bill
rather than one line of it.

### 69. The plan's deliverable 12 names an enforcement that cannot read FL-038's Files table  `[medium]` — **raised by the S5 review**

`TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` recognises one citation shape,
`` `path/to/file.go:Symbol` `` in a single inline-code span (`scripts/docs_test.go:643-654`,
`citedGoSymbol`). FL-038's Files table writes the path in one span and the symbols in separate spans
with no `file.go:` prefix, so **none of its 197 symbol citations is read by that test**. Driven:
adding `ThisSymbolDoesNotExist` to the `event/projection/page.go` row left the test green. A scripted
presence check over all 22 file rows confirms every one of the 197 symbols currently exists, so this
is an absent guard rather than live drift — but it is the guard deliverable 12
(`EVENTSOURCE_P3_PLAN.md:3117`) names, and it affects every flow's Files table rather than only
FL-038. Owed: either the table adopts the shape the walker reads, or the walker learns the table's
two-span shape, plus the re-driven mutation as the proof.

### 70. The settlement's `AfterApply`/one-below/unconfirmed halt is in the plan and the code and in neither table a reader consults  `[medium]` — **raised by the S5 review**

`event/projection/pass.go:406-407` halts when a save was never confirmed, the row did not move, and
the mode is `AfterApply` — §UC-104's Must-not, because the handler's rows are already committed
outside any unit and re-applying is a duplicate the resolution must not choose to make. D10b's table
in the plan has it (`EVENTSOURCE_P3_PLAN.md:332`). FL-038's seven-row settlement table
(`:133-141`) does not, and the module pages' "Two things still halt, because they are not
contention" names only an absent row and one behind the fence — so a reader of either concludes this
case re-delivers. Owed: the row in FL-038's table and the third thing that halts in both pages.

### 71. The `jobs.Stager` outbox asymmetry the reference adjudication owes the module page is on no page  `[medium]` — **raised by the S5 review**

`EVENTSOURCE_REFERENCE.md:452-462` (Reject 2, R26) ends *"That asymmetry belongs on the module
page."* The other five numbered Documentation obligations are discharged — the global `xmin` stall
with both mitigations and the `LOCK … IN SHARE ROW EXCLUSIVE MODE` alternative
(`docs/modules/en/eventpg.md:235-265`), a new projection reading the whole log, the `AfterApply`
two-instance double-apply, the transaction-id cost, and `Spec.Wake` as a hint. This one is not:
`grep -n "Stager\|EnqueueIn\|outbox"` over both projection pages returns nothing. `jobs.Stager` is
this framework's own outbox ([[D-118]]), so a projection publishing integration events is the
obvious consumer, and the page currently describes only the "writes anywhere else" shape — a second
database — which is a different one. Under `AfterApply` the stage commits before the advance, so the
vv exposure is a duplicate enqueue rather than the reference's permanently lost send; the sentence
should say which of the two it is rather than repeat the reference's.

### 72. `Progress.Highest`'s completeness rests on an obligation the `Log` contract does not state  `[medium]` — **raised by the S5 review**

The published sentence — *"once a checkpoint carrying `Highest = P` has been saved, a read resumed
from that checkpoint's cursor answers only positions above `P`, and no event at or below `P` is ever
delivered to that projection for the first time"* — is attributed to the ordering law: *"it means
that much only because the ordering above is a law"* (`docs/modules/en/event.md:392-402`,
`event/checkpoint.go:13-25`). Position ordering is necessary and not sufficient. A store answering
`WHERE position > cursor ORDER BY position LIMIT n` satisfies both [[D-128]] laws, passes
`probe.ascending` and `probe.subsequence`, and still breaks the watermark: position 5 uncommitted
while 6 commits gives a cursor at 6 and 5 is never delivered. What carries the promise is the settled
watermark (`event/eventpg/read.go:214-237`) and the obligation on a `Log` to mint no cursor past a
position a writer could still commit. `Log.ReadAll`'s contract does not name it —
`grep -n "settle\|in flight\|uncommitted\|could still" event/store.go` returns one unrelated line —
and `Reader.checkPage` structurally cannot check it. The obligation **is** certified
(`event/eventtest/sections_resumption.go:acrossTheFlight`, §INV-080's `in-flight-newest-position`
decorator), so a store implementer who runs the suite is caught and one who reads only the contract
is not. Neither published sentence is false — both say "only because", a necessary condition — but
the premise that carries the weight is unnamed. Owed: the second obligation beside the two ordering
laws in `Log.ReadAll`, and both premises in the watermark section of both `event.md` pages.

### 73. The `ErrConflict` row of the failure table lists an outcome the settlement cannot produce  `[low]` — **raised by the S5 review**

`docs/modules/en/projection.md:190` and `docs/modules/ru/projection.md:203` read *"the row is read
once and the settlement decides: a turn, a re-delivery, or a halt"*. A re-delivery is reachable only
through `rolledBack` (`event/projection/pass.go:404-405`), and the arm above it —
`found.Advance+1 == waiting.presented && fenced(waiting.cause)` at `:402-403`, where `fenced` is
`errors.Is(cause, event.ErrConflict)` — takes every refused save at that row first and halts. So
`ErrConflict` yields a turn or a halt and never a re-delivery.

### 74. `event/store.go` moved and is not on the plan's own path list, and two changed kernel files carry no FL-038 row  `[low]` — **raised by the S5 review**

`event/store.go` gained the seven-line [[D-128]] ordering paragraph in `ReadAll`'s contract. The
plan's S5 prose names it (`EVENTSOURCE_P3_PLAN.md:2869`) but "the complete set of paths phase 3 may
add to the manifest" (`:660-686`) does not, so the list every section's checkpoint asserts against
and the file that moved disagree. The move was fenced with its own predecessor
(`.git/event_kernel_before_d128` exists), so it was not absorbed silently — the list is what was not
updated. Separately, `event/store.go` and `event/eventmemory/log.go` both changed for phase 3 and
both carry only an FL-036 row in the reverse `By file` index (`docs/ai/flows/Index.md:471`, `:485`);
`log.go` is where the checkpoint rows now live and FL-038 says so in prose (`:149-150`) without
giving the file a row of its own.

### 75. §INV-021's enumeration is eight rows and one sentence still says seven  `[low]` — **raised by the S5 review**

The eighth hand-off was appended correctly and the paragraph above it updated to "exactly **eight**
points: six carry payload bytes, two are the slices". The sentence two lines below still reads "**The
store boundary — §D.14's eight methods — contributes exactly four of the seven**"
(`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md:3124`). The claim it makes is still true;
the count it names is the phase-1 one.

### 76. The reference's Reject 3 acceptance — every projection reads every event — is on no page and in no entry  `[medium]` — **raised by the phase-3 verification gate**

`EVENTSOURCE_REFERENCE.md:464-472` (Reject 3, R3) refuses a filter parameter on `Log.ReadAll` and
ends with what vv accepts in its place, in as many words: *"every projection reads every event, so N
projections cost N × the read traffic. If that ever becomes the binding cost, the answer is a shared
reader fanned out to N routers — not a filter on the store contract."* Every other obligation the
adjudication leaves behind is either discharged or scheduled — the five numbered Documentation
obligations are on the module pages in both languages, R12's *what vv must prove instead* is
[[D-130]]:22, Reject 2's outbox asymmetry is §71 above. This one is in no module page, no decision,
no flow and no backlog entry: `grep -rn "reads every event\|N × the read\|shared reader" docs/`
returns nothing.

The consequence is operational rather than behavioural, which is why it is `[medium]`: a deployment
that adds its tenth projection to a busy log has multiplied the read traffic on `events` by ten and
nothing in the documentation told it that would happen, and the escape hatch that is *not* a filter
on the store contract is exactly the thing a reader will reach for a filter to solve. It belongs
beside the `Router` section of `docs/modules/{en,ru}/projection.md`, in one paragraph: the cost, and
the shared reader as the answer if it ever binds.

## P2 — the read path should carry the writing transaction's xid8  `[high]` — **REFUSED by [[D-128]], 2026-09-09. Not implementable as written.**

**Do not pick this up. The mechanism it describes is decided against and the decision is
binding.** Read `docs/ai/decisions/D-128-the-log-delivers-in-position-order.md` first; what
survives of this entry is the operational note at the bottom, which is owed for the walk vv
already has, and the two defects, which are the argument for reworking `read.go` some other way.

The entry as written said "**No kernel change**, no change to the phase-3 checkpoint contract."
That was wrong, and it was wrong about the load-bearing half. The reference's read is
`ORDER BY TRANSACTION_ID ASC, ID ASC`, and **the order is the mechanism** — `ID` is vv's
`position`, so positions go backwards inside a page. There is no version of this technique that
keeps position order: taking the column and the index without the order buys nothing at all.

**Why it is refused, in one paragraph.** The reference's order carries a precondition it never
states: the writing transaction takes its id *at the append and nowhere earlier*. The reference
satisfies it structurally — `@Transactional` opens the transaction, `Propagation.MANDATORY` keeps
every half inside it, and the aggregate CAS is the transaction's first write — so for one
aggregate, id order, lock order and version order coincide. vv's store **joins a transaction it
did not open** ([[D-118]]) and is forbidden to open one ([[D-126]]), so the caller's transaction
may have been stamped by an unrelated write long before the append. Measured on PostgreSQL 17.9
over `eventpg`'s own shape: an append at version 2 from a transaction stamped at xid 242922
sorts **before** the append at version 1 from a transaction stamped at 242923, so the tuple read
hands a projector version 2 of one stream before version 1. That breaks `probe.subsequence`
(`event/eventtest/sections_read.go:47-59`), which is a stronger guarantee than the ascending
positions the entry knew about, and it breaks it silently. The control — the same interleaving
with no earlier write — delivers in order, so the reference is right about its own system.

**What would reopen it:** [[D-118]] being superseded so the event store opens the append's
transaction. Nothing less. D-128 §"What would change the answer" is the list.

**What is still owed from this entry, and is unaffected by the refusal.** `pg_snapshot_xmin` is
the GLOBAL oldest running xid across the whole database, not just the event tables. Any
long-running or idle-in-transaction session anywhere — a slow unrelated read, a leaked connection
— holds `xmin` low and stalls every subscription. Events are never lost; delivery freezes. That
belongs in the module page beside the guarantee, with the mitigations:
`idle_in_transaction_session_timeout` and `statement_timeout` on the application role, and
monitoring the age of the minimum unprocessed xid8. It is tracked as `## From the reference` §3
and is S5's.

**And the two defects the entry opened with stand as an argument for reworking
`event/eventpg/read.go`, not for this order.** The watermark walk rests on five composing facts
and has already produced two serious defects under review: `ReadAll` declaring a gap settled from
a floor read in a separate statement and advancing the cursor past a committed event, and a walk
inside a caller's transaction stalling permanently on the first burnt position. A gap-free read
that is *also* in position order exists and the reference documents it without implementing it —
`pg_sequence_last_value` plus `LOCK … IN SHARE ROW EXCLUSIVE MODE` in its own transaction,
README §4-7-2 — at the cost of blocking all writes once per poll. See `## From the reference`
R18. That is the direction a successor entry takes.

## From the reference

Adjudicated in [`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md) against
`github.com/eugene-khyst/postgresql-event-sourcing` @ `90faafb` (`/tmp/pges-ref`) and its Kotlin
port at `photon/new/kotlin/platform-eventsourcing`. Every entry here is a `→` verdict from that
file: a technique vv does not have, recorded with the reference site, the failure it prevents and
the mechanism in full — not at a level of abstraction where it would have to be re-derived. The
`✔` and `✗` verdicts are not repeated here; read the adjudication for those.

Two entries elsewhere in this file are the same subject seen from another side and are **not**
duplicated: `## P2 — the read path should carry the writing transaction's xid8 [high]`, whose
"No kernel change" scoping the adjudication corrects (§1 below), and `## P3` §6/§27, the
unstated `Save`-failure retry rule, which §2 answers from the reference.

### 1. The `## P2 [high]` xid8 entry is scoped wrong in the one respect that blocks phase 3  `[high]` — **CLOSED by [[D-128]], 2026-09-09**

**Settled.** Position-ascending delivery is a **kernel law**, not a store capability; one
stream's order being a subsequence of the log's is the second half of the same law and is the
one the tuple read actually breaks; `Progress.Highest` **keeps** its completeness meaning and
gains a testable statement of it; the ascending check in `Reader.checkPage` stays a refusal for
every store. The four sites were updated in place rather than frozen as they stood, S5 now
freezes the decided contract, and the `## P2` entry above is marked not implementable as
written. `docs/ai/decisions/D-128-the-log-delivers-in-position-order.md` carries the argument,
the live measurement and its control. The question below is left as the record of what was
asked.

---

That entry says "**No kernel change**, no change to the phase-3 checkpoint contract." The SQL,
the column, the index and the `vve2` cursor are indeed `eventpg`-only. **The delivery order is
not.** The reference orders `ORDER BY e.TRANSACTION_ID ASC, e.ID ASC`
(`EventRepository.java:87`), and in that order `ID` — vv's `position` — is **not monotone**:

```
tx A: BEGIN; UPDATE ES_AGGREGATE …   -- xid 100
tx B: BEGIN; UPDATE ES_AGGREGATE …   -- xid 101
tx B: INSERT INTO ES_EVENT …         -- ID 5
tx A: INSERT INTO ES_EVENT …         -- ID 6
```

delivers `(100,6)` then `(101,5)` — positions 6 then 5. Four vv sites forbid that page:

- `event/reader.go:94-98` — `ErrBackend`, "a page whose positions do not ascend"
- `event/store.go:125` — the `Log.ReadAll` contract text, "Envelopes at **ascending positions**"
- `event/eventtest/sections_read.go:24, 61-64, 169` — `probe.ascending`, certified twice
- `event/checkpoint.go:15-17` — `Progress.Highest`: "every committed event **at or below it** was
  delivered", which is false under tuple order

Phase 3's S5 freezes the first three behind `check-event-kernel` and publishes the fourth as a
contract a third-party `Checkpoints` implementer is told to satisfy. **Decide before S3 is signed
off**, and record the answer:

1. is position-ascending delivery a kernel law, or a store **capability** beside
   `MonotoneVisibility` (`event/store.go:32-37`, `event/eventtest/inventory.go:44`)?
2. does `Progress.Highest` mean "the highest position of the page" (safe under either order) or
   "the completeness watermark" (position order only)?
3. is `checkPage`'s real invariant "positions ascend" or "the cursor tiles the log"? Its own
   comment (`reader.go:74-76`) says the purpose is that a consumer never checkpoints past an
   event it never saw — which the tuple read guarantees by construction, more strongly.

If the answer is "the kernel keeps position order", the `## P2 [high]` entry is **not
implementable as written** and must say so, because abandoning position order is the whole of
the reference's mechanism.

### 2. Two live instances of one projection name are never tested, and `AfterApply` double-applies before the fence fires  `[high]` — **CLOSED by [[D-133]] and S3's review, 2026-09-09. The doc row below was wrong and is rewritten.**

**Settled.** The live case exists and is green:
`TestTwoLiveInstancesOfOneNameOverOneSchema`
(`event/eventpg/projection_integration_test.go`) runs two `Projection` values of one name over one
schema in **both** modes, with each instance's first save held at a gate until both have issued
one — so the contention is driven and a run where one instance drained the log before the other
woke **fails** rather than passes. Measured on PostgreSQL 17.9: under `InUnit` the page both
instances claimed is in **one** row of the read model and the handlers were called for exactly the
log's length; under `AfterApply` it is in **two**. Reordering `claimed` to apply before it saves
takes the `InUnit` handler count from 24 to 26, so the case discriminates.

**The `Conflict` arm was decided the other way from the halting one this entry prescribed.**
[[D-133]]: the loser takes the row the winner left, rebuilds its reader from **that** row's
cursor, drops the page it held and backs off; `ErrOvertaken` reaches `State.Err` on every lost
fence and `Ready` once the losing streak outlasts `Tolerate`. Halting on a lost fence kills one of
the two projections on every rolling deploy, which is not a framework a deployment can use. Only
an **absent** row or one **behind** the fence still halts.

**The owed module-page row, corrected** — it read *"the loser halts and does not resume"*, which
[[D-133]] reverses:

> Under `AfterApply`, two live instances of one projection name both apply the overlapping page
> before the fence fires, so the handler must be idempotent. Under `InUnit` against a store that
> evaluates the fenced save against a tuple it holds — PostgreSQL, at advance 1 by speculative
> insertion and above it by the row lock — the loser is refused before its handler runs and
> applies nothing. **In both modes the loser takes its turn and does not halt**: it adopts the
> row the winner left and carries on, and `ErrOvertaken` on `State.Err` and `Ready` is how a
> deployment is told. `Placement: Singleton` is a promise to the deployment rather than an
> enforcement.

The question below is left as the record of what was asked.

---

Reference: `README §7` runs its acceptance suite as
`docker compose … up -d --scale event-sourcing-app=2`, with a deliberately inexact assertion
(`OrderTestScript.java:193-196`, `hasSizeGreaterThanOrEqualTo(23)`). Its stated reason transfers
verbatim: **single-instance testing never exercises the contention branch at all**, so an
implementation that is wrong at N=2 passes.

S4's list has no concurrent two-instance case — `TestAProjectionResumesThroughASecondValueOverOneBacking`
is *sequential* replacement.

What actually happens today, traced: `event/projection/pass.go:256-261` sends a `Conflict` from
`Tracker.Save` to `refused(…)` (`pass.go:295-304`), which halts. Under `AfterApply`, instances P
and Q both `Load` advance 7, both read the same page, **both call `Handler.Apply`** — the read
model is written twice — then P's fenced `UPDATE … WHERE advance = 7` matches and Q's does not,
so Q halts. The reference prevents the double-apply outright, because its
`SELECT … FOR UPDATE SKIP LOCKED` (`EventSubscriptionRepository.java:33-44`) runs **before** the
read; a zero-row result means "someone else has it" and is a logged no-op
(`EventSubscriptionProcessor.java:48`), not an error.

vv cannot take that lock under `AfterApply`: there is no transaction to hold across the handler
call and [[D-126]] forbids the store opening one. Under `InUnit` vv already has the reference's
mutual exclusion **and** the guard the reference lacks — the fenced `UPDATE`
(`event/eventpg/checkpoints.go:353-364`) takes the row lock, the second writer blocks, re-evaluates
`advance = $3 - 1` under READ COMMITTED, matches 0 rows, and the **whole unit rolls back with the
handler's writes**. The reference's own `UPDATE ES_EVENT_SUBSCRIPTION SET … WHERE SUBSCRIPTION_NAME = :name`
has no such guard and is safe only because the lock is always held.

Owed:

- a live case in `event/eventpg/projection_integration_test.go`: two `Projection` values of one
  name, one schema, concurrent, in **both** modes, asserting out of the database. Control: one
  instance alone reaches `PhaseFollowing` and never halts.
- a delivery row in both module pages: *under `AfterApply`, two live instances of one name both
  apply the overlapping page before the fence fires; the handler must be idempotent, the loser
  halts and does not resume, and `Placement: Singleton` is a promise to the deployment rather
  than an enforcement.* **[[D-133]] reversed the middle clause — see the corrected row above.**
  **Delivered by S5, 2026-09-09**, in the corrected form: `docs/modules/{en,ru}/projection.md`
  carries what each mode costs while two instances overlap — `InUnit` refuses the loser before it
  applies anything against a store that evaluates its fenced save under the row's lock,
  `AfterApply` has both instances apply the overlapping page, and a store that stages its save
  optimistically lets both apply under `InUnit` too — plus the rule that the **cursor** the row
  carries and never the advance alone settles an unconfirmed save, and the obligation that puts on
  a third-party `Checkpoints`.
- a decision on the `Conflict` arm. Halting is defensible. Reload-and-retry is what
  `event/checkpoint.go:190-196` was explicitly built for — *"taking its advance is what makes two
  processes take turns over one checkpoint"* — and what the reference does. The kernel and the
  loop currently disagree; that is the entry. **[[D-133]] took reload-and-retry.**

### 3. The global `xmin` stall is vv's today and is on no module page  `[medium]` — **CLOSED by S5, 2026-09-09**

**Delivered.** `docs/modules/en/eventpg.md` and `docs/modules/ru/eventpg.md` carry it beside the
settled-watermark guarantee, in these terms: the floor is the **cluster's** oldest running
transaction id and not this schema's, so any session anywhere that has written and gone idle in
transaction freezes gap settlement — nothing is lost, delivery freezes, and every projection over
the schema freezes with it because they all wait on the same number. The three mitigations are
named and attributed to the deployment rather than to the library:
`idle_in_transaction_session_timeout` on the application role (the one that matters — without it a
single leaked connection is an unbounded stall), `statement_timeout` for the not-idle-but-very-long
shape, and an alert on the **pair** `Progress.At` / `Progress.Highest`, because a `Highest` that
stops moving while the log grows is indistinguishable at a glance from a halted projection. README
§4-7-2's table-lock alternative is recorded as the escape hatch (§9 below) rather than implemented.
The second half — **a new projection reads the whole log** — is on the same pages beside
`Checkpoint.Fresh()`, and on `docs/modules/{en,ru}/projection.md`.

The original entry follows.


`event/eventpg/read.go:319` reads `pg_snapshot_xmin(pg_current_snapshot())` and `walk.settledAt`
(`read.go:232-237`) passes a gap only once that floor exceeds the minted bound. `pg_snapshot_xmin`
is the **oldest running transaction id in the whole database**, not in the event tables — so any
session anywhere that has written and then gone idle-in-transaction holds the floor down and the
walk cannot pass a burnt gap until that session ends. Events are never lost; delivery freezes.

`docs/modules/en/eventpg.md:141-179` documents the two *local* stalls (a live writer holding the
gap; a walk inside a caller's transaction) and not this one;
`grep -n 'long-running\|idle_in_transaction' docs/` returns nothing.

README §4-9 drawback 3 states the general form and offers no mitigation. The Kotlin port writes
the mitigation down (`EventRepository.kt:101-108`) and that is the version to carry:
`idle_in_transaction_session_timeout` and `statement_timeout` on the application role, plus a
monitored subscription-lag metric ("age of the min unprocessed xid8"). **The obligation survives
§1 unchanged** — the tuple read has the same dependency more strongly, because it defers *all*
delivery rather than only gap settlement.

Also owed on the same page (README §4-8's WARNING block): **a new projection reads the whole
log.** vv's is paged by `MaxRead` and resumable, which is better than the reference's unbounded
first poll, but the consequence is identical — adding a projection to a live deployment replays
every event ever written through its handler.

### 4. A redelivery count that survives a process restart  `[medium]` — **P4**

Reference: none — one poison event stalls a subscription **forever**
(`EventSubscriptionProcessor.java:41-46` is a bare `events.forEach`, the exception rolls the
`REQUIRES_NEW` transaction back, the checkpoint never advances, and the only symptom is a
repeating WARN). vv already closes that liveness failure with `oneAtATime`
(`event/projection/pass.go:181-201`) plus the quarantine sink, which is the Kotlin port's answer
in a better shape.

What vv lacks is the **durable** count. `Projection.attempt` is an in-memory `int`
(`projection.go:29`) reset at every `read` (`pass.go:74`), and `permanent()` reaches its cap via
`this.attempt >= this.spec.Attempts` (`pass.go:253`, default 10). An envelope whose application
**kills the process** — an allocation the handler cannot make, an OOM kill, a supervisor taking
the process down for an unrelated runner — is retried at attempt 1 forever: the cap is never
reached, `OnPermanentFailure` never fires, the sink is never called.

Plan decision **D3 does not answer this**. Its argument is that a handler reading "delivered 14
times across 3 processes" can do nothing `(Stream, Version)` idempotency does not already do —
a statement about the **handler**, where the port's counter is for the **engine's cap**. Its
second half ("a durable count would have to be written on the path whose entire property is that
it wrote nothing") is a real objection, and the port's own answer has a real hole worth
recording: the port writes the attempt row in the **same transaction as the failing handler**, so
a handler that failed by aborting the PostgreSQL transaction (a constraint violation, a statement
error) cannot record its attempt — there is no savepoint around `handleEvent` — and that event
retries forever without ever dead-lettering. Both implementations have an infinite-retry hole;
they are different holes.

The port's mechanism, in full, because the shape is the transferable part
(`V9__eventsourcing_subscription_attempts.sql`, `DeadLetterStore.kt`,
`EventSubscriptionProcessor.kt:76-122`):

```sql
CREATE TABLE ES_EVENT_SUBSCRIPTION_ATTEMPT (
  SUBSCRIPTION_NAME TEXT NOT NULL, EVENT_ID BIGINT NOT NULL,
  ATTEMPT_COUNT INT NOT NULL, LAST_ERROR TEXT,
  LAST_ATTEMPT_AT TIMESTAMPTZ NOT NULL, DEAD_LETTERED_AT TIMESTAMPTZ,
  PRIMARY KEY (SUBSCRIPTION_NAME, EVENT_ID));
CREATE INDEX … ON ES_EVENT_SUBSCRIPTION_ATTEMPT (SUBSCRIPTION_NAME, DEAD_LETTERED_AT)
  WHERE DEAD_LETTERED_AT IS NOT NULL;
```

Five steps per event, in order: (1) skip if already dead-lettered and advance the local position
past it; (2) handle, and on success **delete** the attempt row so a later transient failure starts
at attempt 1; (3) on failure `INSERT … ON CONFLICT (SUBSCRIPTION_NAME, EVENT_ID) DO UPDATE SET
ATTEMPT_COUNT = ES_EVENT_SUBSCRIPTION_ATTEMPT.ATTEMPT_COUNT + 1, LAST_ERROR = :err,
LAST_ATTEMPT_AT = :now`; (4) at the cap set `DEAD_LETTERED_AT` and advance **past** the poison;
(5) otherwise **break**, so the checkpoint advances only to the last good event and ordering is
preserved. The partial index makes "what did we give up on" cheap; the migration comment also
names the operator's stuck query, `dead_lettered_at IS NULL AND last_attempt_at < now() -
interval '5m'`.

The lighter option that fits vv and sidesteps D3's real objection: vv already persists two
cumulative counters on the checkpoint row (`applied`, `quarantined`,
`event/eventpg/schema.go:318-320`) and saves per page. A third column carrying the attempt count
for the page the cursor is about to deliver would be written on the **succeeding** save only — not
on the path that writes nothing. It does not survive a crash mid-page, so it closes part of the
hole. Decide which hole is worth closing.

Keep what vv already does better than either: advancing past a poison event breaks per-aggregate
completeness, and `Progress.Quarantined` records it — `event/checkpoint.go:22-24`, *"Non-zero
means that destination has holes, which is what keeps `Highest` from reading as a completeness
claim."* Neither implementation gives its consumer that signal.

### 5. Write-side command idempotency  `[medium]` — **P4**

Reference: none, and it says so — README §4-9(1), "the exactly-once delivery guarantee is hard to
achieve due to a dual-write… consumers should be idempotent". Its only physical write-side dedup
is `UNIQUE (AGGREGATE_ID, VERSION)`. An at-least-once caller — a retried HTTP POST, a workflow
activity — that re-sends the same non-idempotent command applies it twice, at versions 6 and 7,
both legitimate, undetectable.

The port added a real one (`V4__eventsourcing_idempotency.sql`, `IdempotencyRepository.kt:43-54`):

```sql
INSERT INTO ES_IDEMPOTENCY_KEY (IDEMPOTENCY_KEY, AGGREGATE_TYPE, AGGREGATE_ID)
VALUES (:key, :aggregateType, :aggregateId) ON CONFLICT (IDEMPOTENCY_KEY) DO NOTHING
```

run with `MANDATORY` propagation so the claim commits atomically with the events. Two concurrent
transactions racing one key serialise on the primary-key index — the loser blocks until the winner
commits, then sees 0 rows and is a duplicate. There is no window in which both append. The row
records **which aggregate the key was spent on**, and a redelivery routed to a *different*
aggregate is rejected fail-closed (`CommandGateway.kt:181-195`) rather than returning an unrelated
aggregate's state.

**Not blocked by [[D-118]]** — a dedup claim is not a durable *intent*. vv already ships this
exact shape one subsystem over: `jobs.EnqueueOnceIn(ctx, queue, stager, definition, intent, …)`
(`jobs/queue.go:509`) with `ProducerIntent` (`jobs/identity.go:102`). The event write path has no
equivalent. Cost the port carries and does not solve: the key table grows monotonically with no
TTL (only a `CREATED_AT` for a future sweep), and the claim puts a PK-index serialisation point on
the command path.

### 6. Snapshots — the five things the reference gets right, and the one it gets wrong  `[medium]` — **backlog, owner: the phase the roadmap's ~50 ms p99 trigger opens**

The phase-3 plan defers snapshots on a measurement with a recorded re-entry trigger (D-131). This
entry exists so the later phase implements a decision rather than re-deriving it.

1. **Write the snapshot INSIDE the append transaction** (`AggregateStore.java:57`, README §4-4).
   A snapshot committed before or beside the append leaves, after a rollback, a snapshot at
   version 10 for a stream whose head is version 9. Every later load reads it, then reads
   `WHERE version > 10` and gets nothing, and returns state derived from events that never
   committed — permanently wrong, never repaired, because the snapshot is preferred over the log.
   The CAS earlier in the same transaction already holds the row lock, so no two writers race a
   snapshot for one aggregate. `PRIMARY KEY (AGGREGATE_ID, VERSION)` makes a duplicate impossible.
2. **Cadence is `finalVersion % N == 0` with `N >= 2`, enforced twice** — `@Min(2)` at bind time
   *and* a runtime `nthEvent > 1` check (`EventSourcingProperties.java:19,30-35`,
   `AggregateStore.java:62-71`); the reference gives no reason for the redundancy. `N == 1` turns
   the event store into a state store with an audit log attached; `N == 0` is a divide-by-zero on
   the write path. Note the consequence the reference accepts: a multi-event command **jumps**
   boundaries, so worst-case replay length is not bounded by N.
3. **Time travel: the newest snapshot AT OR BELOW the requested version.**
   `WHERE s.AGGREGATE_ID = :id AND (:version IS NULL OR s.VERSION <= :version) ORDER BY s.VERSION
   DESC LIMIT 1` (`AggregateRepository.java:75-94`), then a forward read
   `AND (:fromVersion IS NULL OR VERSION > :fromVersion) AND (:toVersion IS NULL OR VERSION <=
   :toVersion) ORDER BY VERSION ASC`. The half-open/half-closed asymmetry is deliberate: the
   snapshot already includes its own version. **The Kotlin port dropped the `<= :version` clause**
   and pays a full replay from event 1 for every historical read; for an engine whose product
   value is revision history that is the wrong trade. Without the clause, a read at version 12 of
   an aggregate snapshotted at 30 returns **future state labelled as version 12** — and that is the
   path the async integration-event sender takes on every event (§7).
4. **Snapshots are versioned and NEVER upcast; on drift they are DELETED and the aggregate is
   rebuilt from events** (port `V2:27,40`, `AggregateStore.kt:32-34, 59-78`,
   `AggregateRepository.kt:64-89`). The delete is what stops every subsequent load re-detecting
   the same stale row and paying a full replay. Writes use `ON CONFLICT (AGGREGATE_ID, VERSION)
   DO UPDATE SET JSON_VERSION = EXCLUDED.JSON_VERSION, JSON_DATA = EXCLUDED.JSON_DATA` — not
   `DO NOTHING` — so a re-snapshot genuinely replaces a drifted one. The reference has **no**
   schema version on either table, which is its single largest correctness gap for a long-lived
   deployment: rename a field, deploy, and old snapshots deserialize with the field silently
   null while the tail events replayed on top do not restore it. vv already has the *event* half
   — `revision integer NOT NULL CHECK (revision > 0)` (`event/eventpg/schema.go:258, 275`) with a
   declared upcaster chain (`event/chain.go:52,88`) — and would owe only the snapshot half.
5. **The reference has a latent bug here; do not copy its code.** `AggregateStore.java:53-58`
   calls `createAggregateSnapshot` **inside** the per-event append loop while testing the
   aggregate's **final** version (`:66`, already advanced by `applyChange`, `Aggregate.java:67`).
   A command emitting 2+ events whose final version is divisible by N therefore executes the
   snapshot `INSERT` once **per event** with identical `(AGGREGATE_ID, VERSION)`, and
   `AggregateRepository.java:64-72` is a bare `INSERT` with no `ON CONFLICT` — so the second
   violates `PRIMARY KEY (AGGREGATE_ID, VERSION)` and aborts the whole command transaction. It is
   invisible only because every sample command emits exactly one event. The port moved the call
   after the loop (`AggregateStore.kt:184`), which is right.

Neither implementation ever prunes snapshots by age; `ES_AGGREGATE_SNAPSHOT` grows one full-state
row per N events forever and for a large aggregate can exceed the event log.

### 7. An integration event is the aggregate re-read AT the event's version  `[medium]` — **backlog, with §6**

`OrderIntegrationEventSender.java:31-38`:
`aggregateStore.readAggregate(type, event.getAggregateId(), event.getVersion())` — the third
argument is what makes §6(3) a **correctness** requirement rather than a debugging convenience.
Reading head state instead: the subscription is a second behind, the aggregate has moved from
ACCEPTED to COMPLETED, and the `OrderAccepted` integration event goes out carrying COMPLETED.
Consumers see the state machine out of order, and with a large backlog (a new subscription
replaying all history) **every** integration event carries head state — the entire replayed
stream indistinguishable from N copies of the current state.

The payload rule beside it (README §3-7, lines 211-218): a domain event is a delta and internal to
the bounded context; an integration event carries the **whole state**, flat, tagged with the
originating event type and the aggregate version. Publishing the raw domain event exports the
internal model, so every internal refactor is a breaking change for other services; and a
delta-shaped payload is unusable under at-least-once redelivery — replaying `PriceAdjusted(+10)`
twice adds 20.

vv has no at-version load: `event/repo.go:28` folds to head.

### 8. `LISTEN`/`NOTIFY`, if a `Spec.Wake` producer is ever written  `[low]` — **backlog**

Phase 3 does not deliver it and the seam is `Spec.Wake` (`event/projection/spec.go:71`). vv's
`follow` (`event/projection/projection.go:183-192`) already selects on `ctx.Done()`, `drain`,
`Wake` **and** the idle ticker together — the combination the reference structurally **cannot**
express, because `polling` and `postgres-channel` are mutually exclusive
`@ConditionalOnProperty`s (`ScheduledEventSubscriptionProcessor.java:13` vs
`PostgresChannelEventSubscriptionProcessor.java:22`). That absence is the reference's **lost
wake-up**: instance A holds the checkpoint lock and its snapshot predates event E's commit; E
commits, NOTIFY fires, B's `SELECT … FOR UPDATE SKIP LOCKED` returns zero rows, B logs at DEBUG and
returns; A commits without E; the trigger fires on INSERT only, so E sits undelivered until some
unrelated event of the same aggregate type is written. Neither implementation closes it.

The semantics to carry, verbatim, because none is recoverable from a NOTIFY tutorial:

- The trigger (`V2__notify_trigger.sql:1-17`, byte-identical in the port's `V3`):
  `AFTER INSERT ON ES_EVENT FOR EACH ROW`, body
  `SELECT a.AGGREGATE_TYPE INTO aggregate_type FROM ES_AGGREGATE a WHERE a.ID = NEW.AGGREGATE_ID;
  PERFORM pg_notify('channel_event_notify', aggregate_type);`. One channel for the whole system;
  the **payload is the aggregate type**, and that is the design, not a detail.
- **Notifications are delivered only at COMMIT.** A `BEGIN; pg_notify(…); ROLLBACK;` delivers
  nothing, so a notification never announces an uncommitted event. This is also what makes the
  `xmin` deferral self-healing: when a long writer finally commits, its own NOTIFY fires at that
  moment and re-wakes the listener for everything deferred behind it.
- **Identical `(channel, payload)` pairs within one transaction are COLLAPSED** — verified: five
  inserts in one transaction produced exactly one notification. A 500-event command yields one
  wake-up per distinct aggregate type, not 500. Distinct payloads are each delivered. This is the
  reason the payload must be low-cardinality; an event id or aggregate id would produce one
  notification per row.
- **The listener must use it only as a filter and drain durably regardless.** The reference proves
  it does not depend on NOTIFY by shipping polling as a drop-in alternative producing identical
  results.
- **Drain every handler once on (re)connect, before entering the notification loop**
  (`PostgresChannelEventSubscriptionProcessor.java:64`; the port's comment: *"so we never miss a
  NOTIFY that fired before we were listening"*). vv's idle ticker gives this for free; a producer
  written as if `Wake` were the delivery mechanism loses it.
- **The connection must be dedicated, unpooled and long-lived** — `DriverManager.getConnection(…)
  .unwrap(PgConnection.class)`, a daemon single-thread executor, `LISTEN channel_event_notify`,
  then `getNotifications(0)` in a loop, wrapped in an outer `while (isActive())` that reopens and
  re-LISTENs after any failure (`…java:30-40, 50, 101-107`). A pooled connection is reset or handed
  to someone else and the LISTEN registration is silently gone — the subscription goes dark with
  no error. A non-zero `getNotifications(timeoutMillis)` blocks statements from other threads on
  that connection (README:499-501), so it cannot be shared. It is invisible to pool metrics and
  must be added to capacity planning.
- **Shutdown**: there is no way to interrupt a notification poll but by closing its connection
  (`…java:69-74`, the comment says so). `future.cancel(true)`, then `conn.close()` on a volatile
  field, then a 5-second `CountDownLatch`; `isActive()` re-asserts the interrupt flag so the check
  is non-destructive, and an `if (isActive())` guard around the error log keeps every shutdown from
  printing a misleading stack trace.
- **Costs.** A PL/pgSQL call plus one indexed `SELECT` on the writer's critical path, per row —
  the reference gives no reason for choosing `FOR EACH ROW` over a statement-level trigger. And the
  async notification queue is a fixed cluster-wide ring (`max_notify_queue_pages`, 1048576 pages =
  8 GB on PG17): a listener that stops consuming eventually makes **writers'** COMMITs fail.

### 9. The table-level-lock outbox, as the escape hatch from the `xmin` stall  `[low]` — **backlog, situational**

README §4-7-2, documented and never implemented (`grep -rn 'pg_sequence_last_value\|SHARE ROW
EXCLUSIVE' /tmp/pges-ref` finds nothing in code; README:429-431: *"The transaction ID solution is
used by default as it is non-blocking."*). Recorded because it is the **one** alternative that
bounds delivery by the slowest **writer** rather than by the oldest transaction anywhere in the
database — which is exactly the failure §3 documents.

Five steps, in order, each load-bearing:

1. `SELECT pg_sequence_last_value('ES_EVENT_ID_SEQ')` — the most recently **issued** id.
2. `LOCK ES_EVENT IN SHARE ROW EXCLUSIVE MODE`. The mode is the technique: every `INSERT` holds
   `ROW EXCLUSIVE (RowExclusiveLock)`; `SHARE ROW EXCLUSIVE (ShareRowExclusiveLock)` conflicts with
   it **and** is self-exclusive, so acquiring it proves every in-flight insert has finished and only
   one waiter can hold it.
3. It must be taken in a **separate** transaction (`REQUIRES_NEW`) containing **only** that command,
   committing immediately to release it (README:455-456) — otherwise it is held for the whole
   read-and-handle batch and all writes stop.
4. Acquiring and releasing it proves there are no uncommitted writes with an id `<=` the value from
   step 1.
5. Read `WHERE ID <= :lastValue`, with **no** `xmin` guard.

Cost, and why the reference does not default to it: it blocks **all writes** to the event table once
per poll, for as long as the slowest in-flight append. Reading up to `pg_sequence_last_value`
*without* the lock is the naive outbox and loses events; the lock is what turns the sequence
high-water mark into a safe point.

### 10. A gap-free ordered read plus an unordered sink is an unordered system  `[low]` — **backlog, doc, owner: whoever writes an integration-event sender**

Two lines in the reference's config and code, neither commented, which are the only reason its
careful `(TRANSACTION_ID, ID)` ordering survives the last hop:

- `spring.kafka.producer.properties.max.in.flight.requests.per.connection: 1`
  (`application.yml:8-11`). At the default of 5 with retries enabled, a transient failure on the
  request carrying `OrderAccepted` while `OrderCompleted` is already in flight puts the retry
  **after** the later message, permanently. (`enable.idempotence=true` would allow 5 in flight with
  the same guarantee; the reference does not set it.)
- every message keyed by the aggregate id (`OrderIntegrationEventSender.java:43-47`,
  `KafkaTopicsConfig.java:17` `partitions(10)`), so all events of one aggregate hash to one
  partition. Unkeyed, v1 and v2 land on different partitions and consumers see them in arbitrary
  order.

The consumer-side counterpart: the integration event carries the aggregate **version**
(`OrderDto.java:42-43`, from `order.baseVersion`), dense and strictly increasing per aggregate under
`UNIQUE (AGGREGATE_ID, VERSION)` plus the CAS, and the consumer's rule is
`if (incoming.version <= stored.version) drop;`. `event_timestamp` is **not** usable for this — it
is `OffsetDateTime.now()` from the application (`Event.java:111`), a writer's wall clock. vv's
`Envelope` already carries `Stream` and `Version` for this purpose (`event/store.go:87-111`) and its
`RecordedAt` is at least a database clock (`statement_timestamp()`, `event/eventpg/append.go:123`).

Per-aggregate ordering in the log is **derived, not enforced**, and the README never states the
derivation: the CAS `UPDATE ES_AGGREGATE … WHERE VERSION = :expected` runs **first**, before any
insert, so a writer of version N+1 cannot have read version N until N's transaction committed and
does not acquire an xid until that UPDATE executes — hence `xid(N) < xid(N+1)` always. The property
is fragile in one specific way worth carrying: **any earlier write in the same transaction that
assigns an xid before the CAS blocks would break the argument.** vv's append has the same shape —
the CTE's `streams` write is the first write of the statement — and the same fragility.

### 11. Partitioning an event store by time is incompatible with the constraint OCC rests on  `[low]` — **backlog, warning**

Reproduced on PostgreSQL 17.9 against the port's own schema: the port's
`V11__eventsourcing_partitioning_conversion.sql:58-60`
`CREATE TABLE ES_EVENT (LIKE ES_EVENT_legacy INCLUDING ALL) PARTITION BY RANGE (CREATED_AT)` **fails**
with `ERROR: unique constraint on partitioned table must include all partitioning columns / DETAIL:
PRIMARY KEY constraint on table "es_event" lacks column "created_at"`. `INCLUDING ALL` copies both
the PK and `UNIQUE (AGGREGATE_ID, VERSION)`, and PostgreSQL requires the partition key in every
unique constraint. The script is shipped entirely commented out and marked "run MANUALLY by a DBA",
because `ALTER TABLE … ATTACH PARTITION` takes `ACCESS EXCLUSIVE`.

vv has exactly the constraint at risk — `events_stream_version_key UNIQUE (family, key, version)`
(`event/eventpg/schema.go:263`), which [[D-126]] names as what holds if the admission predicate is
ever wrong and which verification refuses to start without. Widening it to include a partition key
**destroys** the guarantee: two rows at one `(family, key, version)` could exist in two partitions.
The only other routes are moving the uniqueness to an unpartitioned side table, or relying on the
`streams` CAS alone. The reference does not partition at all, which on this evidence is the safer
position. The port's `PartitionMaintenanceJob.kt` also pre-creates `preCreateMonths` (default 2)
months ahead and swallows its failures — including the failure that means the parent was never
converted — and `preCreateMonths = 0` makes every insert fail at midnight on the 1st.

### 12. Archival must interlock with checkpoints and snapshots, or it deletes undelivered events  `[low]` — **backlog, warning**

vv has no archival. The port added one (`EventArchiveService.kt`) whose candidate query is
`SELECT ID, CREATED_AT FROM ES_EVENT WHERE CREATED_AT < :cutoff ORDER BY CREATED_AT, ID LIMIT 10000`
(`:218-232`) followed by `DELETE FROM ES_EVENT WHERE ID IN (:ids)` (`:266-274`), checking **nothing**:

- not `ES_EVENT_SUBSCRIPTION.LAST_EVENT_ID` — a subscription stalled behind a long transaction,
  dead-lettered, or newly added has its undelivered events deleted and never receives them;
- not `ES_AGGREGATE_SNAPSHOT` — an aggregate without a snapshot becomes unloadable, and the port's
  own `loadAtVersion` replays from event 1 and asserts the final version, so time travel over an
  archived range throws rather than degrading;
- not the attempt table, which is left referencing event ids that no longer exist;
- and it deletes exactly what a future new-subscription backfill needs, which is the headline
  advantage of event sourcing (README:231-232).

Its class doc claims "Runs inside a transaction — a DELETE failure rolls back" while the class
carries no `@Transactional`, so each JDBC call autocommits. Its SHA-256 is computed over
`JSON_DATA::text` — the JSONB rendering, not the bytes originally written — so it attests the
archive round-trip and not the original payload. vv's `payload bytea` would not have that problem.

The interlock vv would need is against **`checkpoints.cursor` for every projection name**, not
against a time cutoff.

### 13. Event metadata: actor, causation, correlation, bitemporality  `[low]` — **backlog**

`event.Envelope` (`event/store.go:87-111`) carries `Stream`, `Version`, `Position`, `Type`,
`Revision`, `Payload`, `RecordedAt` and nothing else. The port added `METADATA JSONB` (`V8`)
carrying actor, `parent_event_id` (causation), `correlation_id`, `operation_id` and bitemporal
`effective_at`/`recorded_at`; the reference has no metadata column at all and puts `createdDate`
inside the payload via `OffsetDateTime.now()` (`Event.java:111`) — an application clock, not
comparable across instances. Out of phase-3 scope, recorded because a column is cheap at a schema
version and impossible to backfill.

### 14. The reference's index set, as a ceiling rather than a model  `[low]` — **backlog, note**

`ES_EVENT` carries five index writes per append (`V1:10, 16, 19-21`) and two of them back nothing:
`IDX_ES_EVENT_AGGREGATE_ID` is fully redundant with the leading column of
`UNIQUE (AGGREGATE_ID, VERSION)`, and `IDX_ES_EVENT_VERSION` is a single-column index on values
1..N repeated across every aggregate, with near-zero selectivity and no query that filters on
`VERSION` without also filtering on `AGGREGATE_ID`. Neither is explained anywhere. The same
redundancy is repeated on `ES_AGGREGATE_SNAPSHOT` (`V1:30-31`).

vv has **two**: PK `(position)` and `UNIQUE (family, key, version)`, whose leading columns also
serve the foreign key's referencing side. Adding `(writer_xid, position)` for §1 makes three.
**Three is the ceiling** — this is an append-only table that only grows, and every index is a btree
write on the hottest path in the system.

---

## P4 — partitions, the park, generations and effects

Recorded under the 2026-09-08 policy: `[medium]` and `[low]` are scheduled here and left alone.
Everything below was raised by the phase-4 use-case audit (Round 1,
[`EVENTSOURCE_P4_USECASES_GAPS.md`](EVENTSOURCE_P4_USECASES_GAPS.md)) against
[`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md). The twenty blocking
findings are in that file and are **not** repeated here. Two `[medium]` entries from
`## From the reference` (§4, the durable redelivery count; §5, write-side command idempotency) are
already tagged **P4** and belong to this section too.

### 1. `Readiness.Behind` is an `event.Position` used as a distance  `[medium]`

§5.2 ES-04: `Readiness{Reached bool, Behind event.Position, Quarantined uint64}`, described as *"how
far the furthest-behind partition still is"*. A position is an ordinal on a sequence that burns
values — a rolled-back append and an optimistic-concurrency loser both consume one permanently
(`## From the reference` §1's own account) — so `barrier − highest` is not a count of undelivered
events and is not comparable across two logs or two moments. The type invites `Behind` to be
plotted as lag and divided by a rate. Either it is a distinct type (or a `uint64` named for what it
is), or the module page says the number is a hint whose unit is "positions, including burnt ones".

### 2. `Observe` takes both an `Identity` and an `over []Partition`, and the `Identity` carries a `Partition`  `[medium]`

`func Observe(ctx, checkpoints, of Identity, over []Partition) (Barrier, error)` — `of.Partition` is
a field of the value passed as the *first* half of the key, and `over` is the set the `min` is taken
across. Which one wins for the row lookups is unstated, and `Identity{…, Partition: Whole()}` beside
`over: []Partition{{0,3},{1,3}}` is a legal call with no obvious meaning. `Reached` has the same
shape with `arriving Identity` plus `over`. Either the function takes a projection name and a
generation rather than an `Identity`, or `of.Partition` is documented as ignored and refused when
non-`Whole`.

### 3. `Batch` and `State` carry both `Projection string` and `Identity Identity`  `[low]`

§5.2's `Batch{Projection, Identity, Envelopes, Attempt}` and `State{Projection, Identity, …}` carry
the projection name twice, once inside a struct that also holds it. Kept for source compatibility,
which is a good reason — but nothing says the two can never disagree, and a handler that switched to
`Batch.Projection` while resolving its table from `Batch.Identity.Generation` (§1.5's mechanism 3)
reads two halves of one key from two places. Owed: a sentence saying `Projection` is
`Identity.Projection` and is retained for compatibility, or a deprecation.

### 4. The applied-position refusal rests on one clause that is wrong  `[medium]`

§1.4 refuses a second, lower watermark on the ground that *"the moment an operator evicts a letter
unapplied, the hole is permanent and below any such watermark, and the number becomes a lie"*. A
watermark that **stops** at the earliest permanent hole and never moves again is not a lie; it is the
truthful and useful statement *"everything below this was applied, and above it there is at least one
hole"* — which is exactly what an ES-05 `Wait` will need and exactly what
`Progress.Quarantined != 0` cannot answer, because a count says nothing about *where*. The refusal
may still be the right call (it is a new persisted number, and [[D-129]] constrains what it may be),
but the reason recorded should be the real one — a new column on a frozen kernel row — rather than
one that misstates the alternative. ES-03's *«успешная applied-позиция считается отдельно»* is the
appendix sentence at stake; whichever way it goes, the phase-5 owner reads this paragraph.

### 5. `RedriveSpec.Park` is a `Redriver` while `Spec.Park` is a `Park`  `[low]`

Two fields named `Park`, in two specs, of two unrelated types, in one package. A composition root
that wires both writes `Spec{Park: q}` and `RedriveSpec{Park: q}` where `q` must implement both
interfaces or must be two values. Name the redrive's field for what it takes.

### 6. A split committed under a running parent copies a stale cursor, and the re-delivery is unstated  `[medium]`

§1.3 step 2 reads the parent's row and step 3 says *"Both children now stand at the exact point the
parent reached"*. That is true only for a drained parent. A parent still running can commit an
advance between the split's `Load` and its `Forget` (READ COMMITTED, one transaction, three
statements), so the children start behind what the parent applied and re-deliver its last pages —
legal at least once, invisible in §UC-139, and the one consequence an operator who skipped the drain
will actually observe (the halt is the other). Under `AfterApply` the parent's page was applied and
is applied again by a child. Owed: one sentence in §1.3 and an arm in §UC-139.

### 7. `Spec.Pace` has no stated precedence against `Idle`, `Backoff` and `Ticks`  `[medium]`

§UC-167 says `Pace` applies *"while draining"* and *"must not apply while following (where `Idle`
already governs)"*, driven through the injected `Ticks`. Unstated: what happens when `Pace < Idle`,
whether a backoff after a failed pass is `max(Pace, Backoff)` or sequential, whether `Pace` is
measured from the start or the end of a pass (`fixedRate` vs `fixedDelay` — the reference's R27
distinction, which phase 3 decided deliberately for `Idle`), and which ticker the pacing uses.
`Pace` is the whole of ES-04's *«Rebuild получает отдельный ресурсный бюджет»*, so its interaction
with the two throttles that already exist should be written rather than discovered.

### 8. §UC-168 is a measurement, not a falsifiable use case  `[low]`

*"Eight independent walks are issued … the read traffic is eight times a single projection's. This
is measured and recorded, not merely stated."* There is no threshold, so nothing can fail it; and
its **Must not** ("no filter may be added to `Log.ReadAll`") is a design constraint already recorded
as Reject 3 rather than a property of this case. It belongs in the plan's measurement list and in
§8's tension 5 (which already asks for the number), not in the use-case set where a green run means
nothing.

### 9. `Partition.Count()` publishes the one arithmetic the phase exists to forbid  `[medium]`

`func (this Partition) Count() int // mask+1, for a reader; never stated` sits in the same package
as a document explaining at length why `hash % N` is corrupting. The first thing a consumer writes
with a count is `hash % count`. If it stays, it needs the comment that says what it may not be used
for; a `String()` on the topology, or `Mask()` alone, carries the same information to a dashboard
without offering the modulus.

### 10. `Whole()` the constructor and `(Partition).Whole()` the predicate  `[low]`

`projection.Whole()` answers a `Partition`; `p.Whole()` answers a `bool`. Legal Go, and one of them
should be `IsWhole` or `Everything`. Same document, same table.

### 11. `Progress.Quarantined` now counts envelopes the destination was never offered  `[medium]`

§5.2 keeps the kernel field *"its name and its meaning: envelopes the destination did not take"*,
and §UC-146 raises it by **two** for a page in which one envelope failed and one was parked behind
it without ever reaching the handler. "Did not take" and "was never offered" are different facts,
and phase 3's `Quarantined` counted only the first. The count is still the right *signal* (holes in
the destination), but `event/checkpoint.go`'s wording and the module page's now describe a narrower
event than the field records. One sentence, in the kernel comment and both module pages.

### 12. `Split` the package function and `Partition.Split` the method are different operations  `[low]`

`func Split(ctx, SplitSpec) (Identity, Identity, error)` performs the durable handoff;
`func (this Partition) Split() (Partition, Partition, error)` performs the arithmetic. One name, two
meanings, one package. `Partition.Children()` or `Partition.Halves()` for the arithmetic would leave
`Split` meaning the handoff everywhere.

### 13. §INV-084's falsifier includes "no test anywhere asserts the fifth"  `[low]`

*"Falsified by … §UC-134's kill campaign asserting the four and asserting that no test anywhere
asserts the fifth."* A test asserting the absence of an assertion elsewhere in the tree is either a
source walk (which is implementable but is not what the sentence describes) or unimplementable.
Phase 3 has the shape that works — `TestNoDocPromisesExactlyOnceDelivery` and its self-falsifier —
and this should name it rather than invent a new obligation.

### 14. `Effect.Attempt` has no stated lifecycle  `[medium]`

`Effect{Identity, Envelopes, Attempt int}` — nothing in §1.6 says what increments it, what a
returned error from `Dispatch` does (retry the pass? fail it? classify it?), or whether an effect
failure is a handler failure for the purposes of `OnPermanentFailure` and the park. Since `Dispatch`
runs inside the unit, an error from it presumably rolls the whole pass back, which re-dispatches the
same envelopes on the retry — the case `Attempt` exists to make visible and the spec never states.
§4 has no invariant about an effect failure at all.

### 15. `Redriver.Sequence` returns an unbounded `[]Letter`  `[medium]`

`Sequence(ctx, projection, sequence string) ([]Letter, error)` loads a whole parked sequence into
memory, and a sequence is bounded only by the application's `MaxSequenceLetters` — Axon's default is
1024, each carrying a full `event.Envelope` with its payload. Everything else on the read path in
this repository is paged (`Limits.MaxRead`, `Reader.checkPage`, R22's "bounded on every path"). The
redrive is the operator's path and the bound matters less, but the asymmetry should be a decision
rather than an oversight: either `Sequence` pages, or the contract says the caller's bound is the
only one.

### 16. `Sequencer.Name()` has no stated validity or uniqueness rule  `[low]`

The name is written to every parked letter and compared on redrive (§UC-155), so it is a stored
identifier, and nothing says what it may contain, how long it may be, whether it passes the kernel's
`checkName`, or what two sequencers sharing a name means. `ByStream()`, `Unordered()`,
`OneSequence()` presumably carry fixed names that a `SequenceBy` could collide with — at which point
§UC-155's `ErrTopology` refusal silently stops discriminating.

---

Raised by the phase-4 **plan** audit (Round 1,
[`EVENTSOURCE_P4_PLAN_GAPS.md`](EVENTSOURCE_P4_PLAN_GAPS.md)) against
[`EVENTSOURCE_P4_PLAN.md`](../plans/EVENTSOURCE_P4_PLAN.md). The twelve blocking findings are in
that file and are **not** repeated here.

### 17. `Quarantine` → `ParkSequence` is a behaviour change, not the rename the plan calls it  `[medium]`

The plan's `classify.go` section presents three renames and says *"A consumer meets each as a
compile error"*, and S3 lists `event/eventpg/projection_integration_test.go` as *"the rename only,
so the satellite still compiles"*. The satellite really is rename-only —
`TestAQuarantineIsEnvelopeGranular` writes each payload to its own stream
(`projectioncase_integration_test.go:206-211`), so under `ByStream` each is its own sequence and its
assertions survive. `event/projection/retry_test.go` is not. Its three `Quarantine` subtests
(`:262`, `:311`, `:341`) run under the default `Advance` — `stand.spec` sets none
(`harness_test.go:318-327`), so `AfterApply` — which the plan's new refusal 3 turns into an
`ErrSpec` at construction; and the first of them appends `one two three` to the single stream
`orders/a` and asserts the read model holds `["one","three"]`, which `ParkSequence` now parks. Two
things follow that the plan should say out loud rather than leave to be discovered at S3's `go test`:
the shipped `AfterApply` + envelope-skip configuration is **withdrawn**, not renamed, and the
guarantee `docs/modules/{en,ru}/projection.md:202` teaches (*"Quarantine is envelope-granular and
costs a re-delivery"*) is replaced by a strictly stronger and strictly narrower one. [SPEC] §1.4
argues the withdrawal and is right to; the plan is where it becomes a file list and a page edit.

### 18. `event/projection/lifecycle_test.go:235` carries a second stale `walked < 9` guard  `[medium]`

The plan raises `scripts/projection_test.go:139`'s `files < 9` to 18 and argues it well: *"the arm
exists to prove it read the right tree, and a guard left at 9 while the package holds eighteen files
would pass over a walk that found half of them."* `TestTheProjectionStartsNothingAndReadsNoEnvironment`
holds the identical guard over the identical file set (`if walked < 9`,
`event/projection/lifecycle_test.go:235`) and the plan does not raise it. It will not go red — it
will silently stop being the control it is for, over exactly the nine new files whose goroutines and
`init` functions this phase must not have.

### 19. `TestNoModulusIsAppliedToASequenceHash` cannot see the modulus it forbids  `[medium]`

The AST walk is *"for `%` applied to the result of the package's `hash`"*, over `event/`. `hash` is
unexported (`partition.go`), so the only code that could apply `%` to it is `event/projection`
itself — where nobody would. The `hash % N` that INV-086 is about lives in the application, which
computes its own partition assignment; the framework's answer to that is `Partition.Matches` and
`Cover`, and the walk cannot reach either caller. It has a fixture control and is not vacuous, but
it proves a much smaller thing than INV-086 states. The shape that would close the gap is the
`_examples` showing only the `for _, part := range cover.Partitions()` spelling ([SPEC] §8.7, which
the plan adopts) plus a module-page sentence; the walk should be described as what it is.

### 20. Five invariant rows are proved in part by checks that live in no file and are counted by no arm  `[medium]`

INV-084 (*"a doc check that no page asserts the fifth"*), INV-085 (*"a doc check that no page claims
the key is validated"*), INV-089 (*"a doc check that no page calls a per-partition `Highest` the
projection's progress"*), INV-096 (*"a surface walk asserting no exported `Position` is added beside
`Progress.Highest`"*) and INV-102 (*"a doc check that the honest sentence is present"*) name
obligations, not tests. None appears in a section's Tests list, none is in a counted `-list`
pattern, and `scripts/docs_test.go` — where three of them would live — is in no section's file list.
Each invariant carries other named proofs, so none of them is unproved; but the plan's own rule is
that a proof nothing counts is a proof nothing runs.

### 21. `_examples/event-generations` does not say which log store it runs on, and `_examples/go.mod` is in no file list  `[medium]`

[SPEC] §5.4 asks for *"a `Generations` and a `Park` implemented over PostgreSQL with `crudsql`, a
rebuild beside a live generation, the barrier loop, the cutover and the rollback"*. If the log is
`event/eventpg` — a module — `_examples/go.mod` needs a `require` and a `replace`, and without them
`check-tidy` reports the module *cannot be read at all* (`scripts/checks.sh:283-291`, which runs
`GOWORK=off go mod tidy -diff` in `_examples`). The precedent is
`_examples/event-checkpoints-elsewhere`, which runs an `eventmemory` log against a real PostgreSQL
checkpoint store through `crudsql` and needs no new module; if the new example follows it, say so,
because "over PostgreSQL" reads as `eventpg` and the difference is a red `make check` in S6.

### 22. UC-144's "halts" is two different observable outcomes and the plan picks neither  `[medium]`

[SPEC] §UC-144 says a sequencer panic makes the projection *"halt, **naming the sequencer**"* — which
requires a recover somewhere, since a propagated panic names nothing. The plan says *"A panic out of
a **sequencer** halts and is not recovered into a page failure: `applyPage`'s recovery does not
extend there"*, which reads as no recover at all. The two differ where an operator looks: recovered
into `this.stop(this.haltedBy(...))` gives `PhaseHalted`, `ErrHalted` on `State.Err` and a failing
`Ready`; unrecovered, it unwinds through the caller's `Unit` (`crud.inNewTx` rolls back and re-panics,
`crud/executor.go:623-628`) and is caught by `runtime.invoke` as `ErrRunnerPanicked`
(`runtime/supervisor.go:185-191`) — the runner is gone, `Ready` is never asked, and `Projection.Run`
has panicked, which nothing in this package does today. `TestASequencerPanicHaltsAndAHandlerPanicDoesNot`
cannot be written until this is chosen.

### 23. S2's `event-kernel-moved` allowed set admits any `eventmemory` change the plan says must be reported  `[medium]`

S2 says *"If `event/eventmemory/` appears in the moved set, the report says which file and why — a
store that fails a new obligation is a real finding, not a fix to absorb"*, and then writes an
allowed ERE ending `|eventmemory/[a-z_]*\.go)$`. The arm prints the moved set, so a reader can see
it, but the gate cannot fail on it — which is the difference between a check and a convention, and
this plan's whole kernel-fence argument is that the difference matters. Leaving `eventmemory` out of
the allowed set and adding it deliberately, in the same change as the finding, is the spelling that
matches the sentence.

### 24. The alignment refusal cannot distinguish "not aligned" from "not comparable"  `[medium]`

`mine, err := event.NewAuthority(this.tracker.Backing(), crud.KeyOf(executor));
aligned := err == nil && authority.Same(mine)` collapses three outcomes into one halt: a genuine
two-database wiring, a `KeyOf` that answered something `reflect.Value.Comparable()` refuses
(`event/authority.go:30-32`), and a `Backing` the tracker reports invalid. The first is the
operator's problem and the other two are the framework's. `checkUnit`'s existing four refusals each
name their own cause in plain words; this one should too, or an operator reading *"two resources"*
goes looking for a second database that is not there.

### 25. `Redriver.Claim`'s empty-sequence convention is in the spec and not in the plan's contract  `[low]`

[SPEC] §1.4 states it: *"`Claim(ctx, of, "")` takes the least recently tried unclaimed sequence,
`Claim(ctx, of, "A")` takes that one if it is unclaimed, and both answer `found = false` rather than
an error when everything is claimed."* The plan's `redrive.go` block carries the signature with a
bare `sequence string` and no comment, and `Redrive.Any` versus `Redrive.Sequence` is the only place
a reader would infer it. A `Redriver` implementer working from the contract block will not implement
the empty case.

### 26. `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven` becomes a lie  `[low]`

The plan moves the constant the test asserts (`event/eventtest/checkpoints_test.go:310`, 11 → 13) and
leaves the name, which then says "eleven" about thirteen. `scripts/docs_test.go`'s
`TestEveryTestNameTheDocsCiteExists` makes a rename a doc obligation, so it is worth doing in the
same change rather than later.

### 27. Three line citations in the plan are a few lines off  `[low]`

`event/eventmemory/checkpoints.go:113` is `tx.live()`; the advance-1 fence the plan describes is at
`:117`. `startsNothing` is *called* from `scripts/event_test.go:67`, not `:66` (66 is the func
declaration). `event/eventpg/checkpoints.go:353-364` is the body of `saveStatement`, whose
declaration is at `:353` and whose `UPDATE` arm ends at `:365`. None of these misleads a reader who
opens the file; all three are the kind of drift the repository's own consistency-pass rule exists to
catch.

### 28. The `Effects` contract never names the outbox this repository already ships, and its doc comment blesses the table [[D-118]] warns about  `[medium]`

The audit was asked whether the plan reinvents anything `jobs`, `runtime` or `event/projection`
already provides. `runtime` and `event/projection` are used correctly: the redrive scanner is the
host's `runtime.Every`/`runtime.NewPeriodic` (`runtime/periodic.go:45,75`), `Spec.Pace` rides the
existing `runtime.Ticks` seam, `Projection.Name()`'s duplicate refusal is
`runtime.Supervisor`'s `ErrDuplicateRunner` (`runtime/supervisor.go:67`), and the park is genuinely
**not** a `jobs` queue — `jobs` delivery is *"at-least-once and unordered"* by [[D-118]]'s own words,
and a park is ordered-per-sequence, which nothing in `jobs` models.

`Effects.Stage` is the one that touches it. The plan's doc comment reads *"a staged job ([[D-118]]),
a row in your own tables"*, giving equal standing to the mechanism [[D-118]] §"The outbox already
exists; what was missing was the word" spends a paragraph refusing: *"an application that needs 'this
effect happens if and only if this commit happens' builds a second mechanism — a `pending_effects`
table and a poller — to get a property it already had. Two outboxes over one database is worse than
either alone: the second one has no lease, no fence, no retry budget, no retention and no payload
version."* `EVENTSOURCE_REFERENCE.md` Reject 2 names the shipped symbols — `jobs.Stager`
(`jobs/queue.go:35-38`), `jobs.EnqueueIn` (`jobs/queue.go:483`), `jobspg.Driver.Stager` — and says
the ordering, retry and dead-letter questions *"are questions `jobs` must answer for a staged
integration event, not questions that disappear."* The plan cites [[D-118]] from D-135 and names
none of them, and §UC-178 (*"an effect sink is a staged job"*) is proved by
`TestAStagedJobTheReadModelAndTheAdvanceCommitTogether` without saying what a staged job is there.

Nothing here is a violation — `Effects` is an SPI and the framework writes no table — but the type's
doc comment is the one place a consumer reads for guidance, and it currently points two ways. The
shape that closes it: `Effects`' comment names `jobs.EnqueueIn` through a `jobspg.Driver.Stager` as
the intended sink and marks the private table as the case [[D-118]] warns about; the module page and
D-135 say the same; and §UC-178's test uses whichever it is. If the eventpg live test reaches
`jobs/jobspg`, note that it is a module — the require and replace belong in the same change, and
`costsNoMoreThanItNames` will not see it because `firstPartyDependenciesIn`
(`scripts/extensionlisting_test.go:35-43`) lists without `-test`.

---

Raised by the phase-4 **S1 code review** (Round 1,
[`EVENTSOURCE_P4_S1_GAPS.md`](EVENTSOURCE_P4_S1_GAPS.md)) against the shipped
`event/projection/{identity,partition,cover,sequence}.go` and the `checkUnit` alignment. The
five blocking findings are in that file and are **not** repeated here. Items 9, 10, 12, 16 and
24 above already cover the `Count`/modulus invitation, the two `Whole`s, the two `Split`s,
`Sequencer.Name()`'s validity rule and the alignment refusal's three-outcomes-one-message
problem, and were not re-raised.

### 29. `hash`'s comment says "Published" of an unexported function with no route to reproduce it outside Go  `[low]`

`event/projection/partition.go:132-134`: *"FNV-1a/32 over the UTF-8 bytes of the sequence key.
**Published**, and never changed: changing it moves every key at once."* The function is
unexported and the only exported route to the assignment is `Partition.Matches(sequence)`, which
answers a bool for one partition at a time. An operator who wants to answer "which partition
holds order 4711" in SQL — the natural question during a split or a park drain — cannot, and
nothing on the module pages gives them the algorithm. Either the comment says *documented* and
`docs/modules/{en,ru}/projection.md` carry the algorithm with a worked example, or a
`Partition.Of(sequence string) uint32`-shaped answer exists. The word "published" currently
promises a reproducibility the package does not offer.

### 30. `ErrTopology`'s sentinel text is narrower than the refusals that wrap it  `[low]`

`event/projection/errors.go:26`: *"projection: this topology change is not one this projection
can make"*. It is wrapped by `NewPartition` (a mask that is not a mask — a declaration, not a
change), `ParsePartition` (a parse), `NewCover`'s gap and overlap (a set being written down for
the first time) and, from S2, `Split` (an actual change). `errors.go`'s own comment above the
block already says the wider thing — *"about a set of partitions or a change to one"* — so the
sentinel and its doc disagree by one word. It is [SPEC] §5.2's exact text, so changing it is a
spec amendment rather than a code fix; the cheap alternative is the doc comment saying why the
sentence is narrower than the use.

### 31. `TestNothingInTheProjectionPackageOpensATransaction`'s unit-of-work arm is evaded by an import alias  `[low]`

`scripts/projection_test.go:222-227`: the `crud.InNewTx`/`InTx`/`InAtomic` arm requires
`selector.X` to be an `*ast.Ident` literally named `crud`, so
`import vvcrud "github.com/frostgrove/vv/crud"` followed by `vvcrud.InNewTx(...)` is invisible to
it. The `Begin`/`Commit`/`Rollback` arm has no such escape because it keys on the selector alone.
The fixture control (`transactionShapes`) uses the plain import and therefore cannot see the hole.
The package next door already has the shape that closes it — `checkedPackage` type-checks with
`importer.ForCompiler`, which makes a prohibition about a *type* rather than a spelling
(`scripts/projection_test.go:525-533`) — so resolving the callee's package path rather than
matching the identifier is a small edit against an existing helper.

### 32. The resume-time topology refusal sees a coarser row and never a finer one  `[medium]`

Raised while closing GAP-2 of `EVENTSOURCE_P4_S1_GAPS.md`, and recorded rather than fixed.
`Projection.unclaimed` (`event/projection/pass.go`) asks the store about every **coarser** share
of this runner's key space — `Identity.coarser()`, at most ten rows for a mask of 1023, none at
all for a projection that named no partition — and refuses when one is live. The mirror case is
not covered: a runner at `orders#0.1` started while `orders#0.3` and `orders#2.3` are live is a
topology being made **coarser**, and it is admitted.

**Amended 2026-09-09, closing GAP-2 of `EVENTSOURCE_P4_S2_GAPS.md`.** The stated reason for
`[medium]` was wrong on both halves and no longer applies to the part that mattered. It said the
exposure was "an operator hand-declaring a coarser cover instead of rebuilding, which is a
documented-against action"; it was not documented against anywhere, and the case actually reachable
was not hand-declared at all — `Split` retires the parent's row, so redeploying the release that
ran one release earlier was admitted and replayed the whole log into the live children's read
model. It also said "§1.3 already refuses a merge outright", which was drift: what is absent is a
`Merge` **function**, and the state was admitted in silence.

**What is closed:** every coarser share that `Split` itself retired. The handoff now records the
retirement durably (`<identity>#split`, carrying the parent's cursor, written in the same
transaction), and `Projection.unretired` reads that record at every resume. The refusal names the
row and the two ways out.

**What is still open, and why it stays `[medium]`:** a finer row this framework did not create —
`orders#0.3` hand-declared and live while a runner at `orders#0.1` starts. The finer set is
unbounded, so detecting *that* still needs either a `List` on the `Checkpoints` contract (which
phase 3 froze) or a probe of every descendant name down to `MaxPartitions`, which is 1024 loads per
start. The module page and the release note now state what a rollback does, which is what the old
justification claimed and did not have.

The shape that would close it cheaply is a `Checkpoints.Names(prefix)` — the same call
`Observe` would want in S4 for a `Cover` whose members are discovered rather than declared — and
it is one addition to the store contract, not a projection change.

### 33. `unnameable` duplicates `event.checkName` and only a test holds them together  `[low]`

Raised while closing GAP-3. `event/projection/identity.go:unnameable` spells the kernel's
identifier rule a second time because §5.1 freezes the `event` surface for this phase, so no
checker can be exported to call. `TestNewIdentityRefusesEveryNameTheKernelRefuses` walks both over
one table and over every byte a name can carry, which catches a divergence — but only in the
direction of *behaviour*, and only for single-byte differences and the shapes the table holds. A
multi-byte rune class added to `event/text.go` and not to `unnameable` would be caught only if the
byte walk happened to reach it.

The shape that would close it is exporting the kernel's rule once the surface freeze lifts —
`event.CheckName(string) error` or a `MaxNameBytes`-shaped constant pair — and deleting the
duplicate. It is a surface addition and belongs to whichever phase reopens `event`.

### 34. The zero-value door walk cannot see an `Identity` or a `Cover` carried in a spec struct  `[medium]`

Raised in S2 of phase 4, and recorded rather than fixed. `doorsThatDoNotAsk`
(`scripts/projection_test.go:765-790`) walks an exported function's **parameter list** for a
parameter whose type is `Cover` or `Identity`, so it holds **P-11** for `Observe(ctx, checkpoints,
of Identity, over Cover)` and `Reached(...)` — and not for `Split(ctx, spec SplitSpec)`, whose
identity is a field of the spec, nor for `Cutover(ctx, spec CutoverSpec)`, which is S4's and has
the same shape. `Split` does refuse `Identity{}` with `ErrSpec` and
`TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal`'s "the doors Split refuses before it opens
anything" subtest asserts it, so the rule holds today by a test rather than by the walk.

The shape that would close it is one arm in the same walk: for a parameter whose type is a struct
declared in the same package, report the function when the body never asks about any field of it
whose type is `Cover` or `Identity`. The fixture control beside it already has the two-doors-that-
ask / two-that-do-not shape to extend.

### 35. `durability` is the one checkpoint section no defect names, because its defect is a factory  `[medium]`

Raised in S2 of phase 4 by `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt`, which S2
wrote and which found **seven** checkpoint sections with no defect at all — `binding`, `forget`,
`bounds`, `refusal classes`, `lifecycle`, `concurrency` and `durability`. Six were closed in the
same change (`checkpointDefects` 5 → 14, and `unfenced` moved from `fence` to `concurrency` with
`ahead` taking `fence`). `durability` is exempted, named in `undecorated`
(`event/eventtest/checkpoints_test.go`) so the list can only shrink: a checkpoint defect is a
`func(event.Checkpoints) event.Checkpoints` wrapped around the value the factory built, and what
`durability` asserts is that a value the factory builds **afterwards** reads what the first one
wrote. The store suite spells the equivalent defect as a factory — "claims persistence and builds
a second value over its backing that has none of what the first wrote" — and the checkpoint
harness has no shape for one.

The shape that would close it is a second field on the checkpoint defect row, a
`func(eventtest.CheckpointFactory) eventtest.CheckpointFactory` beside `over`, and one row using
it. `factoriesFor` (`event/eventtest/defects_test.go:128`) is the precedent.

### 36. Two of S2's checkpoint defects break more sections than the one they name  `[low]`

`adopting` ("creates a row at advance 1 over a live one") also fails `fence` and `refusal classes`,
and `retiring` ("forgets outside the caller's transaction") also fails `transactions` — because a
store that is wrong in that one statement really is wrong in every section that asks about it.
`TestEveryCheckpointDefectIsReportedByItsOwnSection` asserts only that the **named** section fails
and that the same store without the defect passes, so nothing reports the spread, and the section
each row names is the obligation it is *about* rather than the only one it breaks.

The shape that would close it is a second assertion in that test — the sections a defect breaks,
listed on the row — which is a table of 14 rows against 14 sections and is worth it only if a
defect is ever added whose spread is a surprise.

### 37. Every operator-facing string in the loop names `Spec.Name` and never the identity  `[medium]`

Raised by the S2 review. Thirteen sites in `event/projection/projection.go` and
`event/projection/pass.go` render `this.spec.Name` into a message that reaches a human:
`Run`'s twice-run refusal (`projection.go:119`), both `Ready` sentences (`:186`, `:188`), the
`ErrOvertaken` transition (`pass.go:560`), `refused` (`:575`), `haltedBy` (`:580`) and the six
`checkUnit` refusals (`:638`–`:658`). With a `Cover` of sixteen partitions, all sixteen runners
publish `"orders"` in every one of them, and the driven halt in GAP-1 above reads
`"orders": … refused a save over the very row its own fence admits` for a runner whose row is
`orders#0.1`.

What keeps it from being higher: `Projection.Name()` *is* the identity
(`projection.go:77`), so a supervisor's own reporting and any log line carrying the runner name
disambiguates, and `State.Identity` (`state.go:19`) gives a programmatic observer the exact
answer. What is ambiguous is the sentence itself, which is what an on-call engineer reads first.

The shape that would close it is one accessor — `this.named()` answering `this.identity.String()`
— and thirteen substitutions. It is deliberately not folded into GAP-1's fix, because GAP-1 is a
row key and this is a rendering, and mixing them would make GAP-1's close criteria unreadable.

### 38. `Quarantined` carries `Spec.Name` and no `Identity`, so a sink cannot tell which partition parked an envelope  `[medium]`

Raised by the S2 review. `Projection.quarantine` (`event/projection/pass.go:341-347`) builds
`Quarantined{Projection: this.spec.Name, …}`. Under a `Cover` of N partitions every sink row is
recorded under one name, and an operator draining the sink cannot answer "which runner refused
this" without re-deriving the sequence key and the mask by hand. `Batch` gained `Identity` in this
same section (`page.go:16`) for exactly this reason; `Quarantined` did not.

Why it is recorded rather than raised: S3 replaces the quarantine sink's role with the park, whose
key is `Identity.Whole()` by §1.4 and plan **P-8**, so the field this wants may arrive with a
different name on a different type. Deciding it here would pre-empt S3's contract.

The shape that would close it is one field, `Quarantined.Identity`, set from `this.identity`
beside the name already there — and a line in `docs/modules/{en,ru}/projection.md` saying a sink
sees the partition.

### 39. Three symbols named `Whole` in one package, with three different meanings  `[low]`

Raised by the S2 review. `projection.Whole() Partition` (`partition.go:33`) is a constructor for
the whole key space; `Partition.Whole() bool` (`partition.go:114`) is a predicate; and
`Identity.Whole() Identity` (`identity.go:145`) is a projection that drops the partition and keeps
the generation. `spec.Partition.Whole()` and `identity.Whole()` sit four lines apart in
`pass.go:135` and would read alike to someone skimming, while one is a question and the other is a
value.

Each name is defensible on its own and the type system keeps them apart, so nothing is wrong
today. The cost lands in S3, where `Identity.Whole()` is the park's key and will appear in
argument position beside `Partition.Whole()` in the same expressions.

The shape that would close it is renaming the predicate — `Partition.IsWhole()` or
`Partition.Everything()` — which is one surface line and touches `pass.go`, `partition.go`,
`cover.go` and their tests.

### 40. `Split` mints `Progress.At` from `time.Now()` with no seam  `[low]`

Raised by the S2 review. `handOver` (`event/projection/topology.go:128`) takes `at := time.Now()`
and writes it into both child rows. `SplitSpec` carries no clock and the framework has no ambient
one, so a caller who runs a split under a frozen clock — a test, a replay harness, a deployment
that stamps its own instants — cannot make the two child rows carry an instant it chose.

It is consistent with `presentSave` (`pass.go:372`), which does the same, and `Progress.At` is
documented in `event/checkpoint.go:34-40` as *"the consumer's own observation"* rather than
anything a resume depends on, so nothing is incorrect. It is recorded because the repository's own
rule against ad-hoc clocks has no exemption written for either site, and because `Spec.Ticks`
already establishes that this package injects its time seams.

The shape that would close it is an optional `Now func() time.Time` on `SplitSpec`, defaulting to
`time.Now`, and the same on `Spec` — one field each, and the two sites read it.

### 41. `ParseIdentity` and `ParsePartition` are exported and called from no non-test file  `[low]`

Raised by the S2 review. `grep -rn "ParseIdentity\|ParsePartition" --include=*.go` over the tree
finds `event/projection/identity.go:104` (`ParseIdentity` calling `ParsePartition`) and nothing
else outside `_test.go`. Both are published surface with no consumer, added in S1 for the
injectivity argument of §INV-097 and for the S3 doors — `Letter.Identity`, `RedriveSpec.Of` — that
will parse a recorded name back.

Recorded rather than raised because the plan declares both in its contract block and the
round-trip property they carry is genuinely load-bearing for a name used as a primary key; a
published parser with a test and no caller is a smaller problem than a name that renders one way
and reads back another. It is here so that if S3 lands without calling either, the question is
asked rather than forgotten.

The shape that would close it is S3 calling them, or — if S3 finds it does not need to — removing
them before the first tag, since after it a disappearing line is a breaking change.

### 42. `ParseIdentity` and `ParsePartition` still have no non-test caller after S3  `[low]`

Item 41 asked the question S3 was to answer, and the answer is that S3 did not need either. The
doors it landed — `Letter.Identity`, `RedriveSpec.Identity`, `Claim.Of` — carry an `Identity`
value rather than its rendering, so nothing in the framework parses a recorded name back: the
framework hands a `Park` an `Identity` and reads one back only from the value it handed over. A
`Park` implementation over SQL does store the rendering (`event/eventpg/projection_integration_test.go`'s
`queue` keys its table on `Identity.String()`), and it compares that string rather than parsing it.

Recorded, not fixed: S4's `Observe`, `Cutover` and `Generations.Active` are keyed by a projection
name and a `Generation`, so they may still reach for the parser. The question is asked again there,
and if S4 and S5 also do not need it, the two functions are a removal to make before the first tag.

### 43. `Projection.opened` exists for one arm of the failure table  `[low]`

Raised while writing S3. `stalled` (`event/projection/pass.go`) restores `this.attempt` to
`this.opened`, the attempt the pass began with, because the isolation pass raises `this.attempt`
on its way in and a blocked pass must consume no attempt (§UC-150). That is one loop field whose
only writer is `once` and whose only reader is one arm of `applyFailed`.

It is correct and it is measured — `TestAFullParkBlocksTheAdvanceAndSkipsNothing` goes red when the
restore is dropped — and the alternative shapes are worse in a way worth stating rather than
absorbing: passing the attempt down through `deliver`/`applier`/`tally` widens four signatures for
one arm, and not raising the attempt for the isolation pass at all changes what `Batch.Attempt`
tells a handler about a re-delivery.

The shape that would close it is folding the entry attempt into `tally` at the point `deliver`
builds one, which is a change to `deliver`'s signature and therefore to every applier.

### 44. §UC-146's "without ever reaching the handler" is exact for one of its two cases  `[medium]`

Raised while writing S3, and recorded in the plan's S3 amendment 3 as well. An envelope whose
sequence the queue **already** holds never reaches the handler at all, which is UC-147 and is
asserted absolutely. An envelope in the same page as the failure that **discovers** the block was
in the whole-page batch that discovered it, and that batch rolled back — under tier A, the only
tier `ParkSequence` constructs at, so the handler's writes for it went back with the advance. From
the moment the failure is known it is never delivered again, which is what
`TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt` asserts through `delivered.redelivered`.

The gap is between the prose and the mechanism rather than in the mechanism: a handler with a
side effect outside the transaction — which is outside the contract §1.1 already states — would
observe the rolled-back delivery. Closing it absolutely means delivering one envelope at a time to
every projection carrying a `Park`, which is page batching gone for the healthy case that UC-148
exists to keep free.

The shape that would close it, if it is ever wanted, is an opt-in `Spec` field that skips the
whole-page attempt when a `Park` is set — paid for by the projections that want it and by no
others.

### 45. `State.Parked` and `PhaseDegraded` are generation-wide, so a partition holding nothing never reports "following"  `[medium]`

Raised by the S3 code review (`EVENTSOURCE_P4_S3_GAPS.md` GAP-7), driven rather than read.
`Projection.counted` asks `Park.Sequences(ctx, this.identity.Whole())` and `followed()` answers
`PhaseDegraded` whenever that count is non-zero, so after a split of a partition holding one
parked sequence both children report it:

```
child orders#0.1: phase=degraded parked=1 quarantined=2
child orders#1.1: phase=degraded parked=1 quarantined=0
```

The second child holds no letter and can never drain the sequence — its mask does not match the
key — and still publishes `Parked: 1` for as long as its sibling's poison order stands. `Ready`
passes and no envelope is affected, so this is a monitoring statement that is wrong about the
runner it is published for rather than a correctness defect. The module page states the `Holds`
cost of the generation-wide key and not this consequence of it.

The shape that would close it is either a sentence in `docs/modules/{en,ru}/projection.md` saying
the two are generation-wide, or a per-partition count — which needs a `Park.Sequences` that takes
the partition, and that is the shape §1.4 refuses because it orphans letters at a split.

### 46. A redrive under a unit that runs its body twice reports the opposite of what happened  `[medium]`

Raised by the S3 code review (GAP-8). A `RedriveSpec.Unit` that calls its body twice, each in its
own committed transaction, leaves the first letter applied and evicted — `rows=[A2]`, one letter
left of two — and `Redrive.letter`'s `case answered != nil` arm answers:

```
answered={Applied:0 Left:2}
err=the unit carrying the letter … did not commit, so nothing was applied and nothing was evicted
```

Both clauses are false. `errLetterRanTwice` correctly stops the second run from re-applying, so
nothing is lost or doubled and a re-run drains from the next letter; only the report is wrong.
The loop has a settlement for the analogous case (`Projection.settle` reads the row back); the
redrive has none and cannot, having no row to read.

The shape that would close it is telling "the body never ran to completion" apart from "the body
completed and the unit then answered", and saying only what is known.

### 47. `Retried.Left` is a snapshot count and its comment claims it is live  `[medium]`

Raised by the S3 code review (GAP-9). `redrive.go` says *"Left is what the sequence still holds
when the call returns"*, and every arm derives it from the slice `Park.Sequence` answered at the
top of the drain. Driven: the loop parks a later envelope into a sequence a redrive is mid-drain
of, and the redrive answers `{Applied:1 Left:0}` over a queue holding one letter. The ordering is
correct throughout — the redrive applied `A1`, the loop parked `A2`, nothing was applied out of
order — so the defect is the number and the sentence.

The shape that would close it is the comment saying `Left` is measured against the letters the
call was handed, plus a case that pins it under that interleaving.

### 48. The module page's `RedriveSpec` example does not compile  `[low]`

Raised by the S3 code review (GAP-10). `docs/modules/en/projection.md` and its `ru` twin write
`Identity: projection.NewIdentity("orders", 2, projection.Whole())` inside a struct literal, and
`NewIdentity` answers `(Identity, error)`. `scripts/docs_test.go` checks that the symbols a page
names exist rather than that its snippets build, so it went green.

### 49. The study's nuance-6 saga clause is carried nowhere  `[low]`

Raised by the S3 code review (GAP-11). §ES-03's source states *"there is no support for using a
dead-letter queue for sagas"*, because a saga's ordering across associations is not expressible
as one sequence identifier. vv's answer exists — `SequenceBy` and `OneSequence` let a caller
widen the key until the ordering is expressible — and is written down nowhere beside
`ParkSequence`, so a handler that correlates across streams under the default `ByStream()` gets a
park that blocks one stream while its correlated partner carries on.

The shape that would close it is one paragraph in `docs/modules/{en,ru}/projection.md`.

### 50. A `Barrier` a caller built by hand is accepted as evidence when the two names agree  `[medium]`

Raised while writing S4. `Barrier` carries three exported fields, so
`Barrier{Projection: "orders", Generation: 1, At: 999_999}` is constructible anywhere, and
`Reached` admits it: its two refusals are that the projection differs from the arriving
generation's and that the generation is the arriving one's own. The zero `Barrier` is caught by
the first, which is what closes **P-11**'s door at this value — but a *plausible* one is not.
`Observe` is the only producer, and making the type opaque the way `Identity` and `Cover` are
would give it a constructor nobody could call. `Cutover` is unaffected: it observes its own and
has no field a barrier reaches it through. What is open is the standalone `Reached`, where an
operator's dashboard could be reading a number somebody typed.

### 51. A cutover refusal names how many holes and never which sequences  `[medium]`

Raised while writing S4. §UC-163 asks the refusal to name *"`Holes` and the sequences behind
it"*. `Park.Holes` answers a number, and the one method that could name the sequences —
`Park.Sequences` — has a contract clause saying it runs **outside** the caller's unit
(`park.go`), which is where a cutover's every read happens. So `holedRead` names the count, the
arriving identity and the redrive that drains it, and an operator finds the sequences through
`Redrive.Any` rather than off the refusal. Closing it needs either a fifth `Park` method whose
contract says "inside the unit", or a documented exception for `Sequences`.

### 52. The first cutover of a deployment whose log is empty is `ErrRetired`  `[low]`

Raised while writing S4. `Cutover` refuses an arriving generation that holds no checkpoint row
for any member of its cover, because a generation whose rows were dropped and one that never ran
read alike from the rows and neither has anything behind the read target. A deployment whose log
is empty has a generation 2 that drained perfectly and wrote no row — an idle projection issues
no saves — so its first cutover is refused with a message about rows that are gone. The refusal
is safe and its text names both readings; what it is not is the message that case deserves.

### 53. `CutoverSpec.Park` is optional, so the holes check is disarmed by omitting a field  `[medium]`

Raised by the S4 review. `Cutover` refuses a nil `Checkpoints` and a nil `Generations` and
admits a nil `Park`, which `reaching` reads as zero holes (`generation.go:205-207`). So an
operator who forgets the field cuts over to a generation holding parked letters and evicted-
unapplied ones with no error and with `AcceptQuarantined` still false — the field whose whole
purpose is to be the only way past that check. The framework has no route to the projection's
own `Spec.Park`, so it cannot infer the answer; what it can do is make the absence deliberate
the way `AcceptQuarantined` is, rather than the default. The shape that would close it is a
declared "this projection has no queue" value, or a refusal that names the omission.

### 54. A cutover under a unit that runs its body twice reports the opposite of what happened  `[medium]`

Raised by the S4 review, driven. `Cutover` derives `from` from the spec rather than from rows
read inside the run, so a `Unit` that runs its body twice — a wrapper that retries a
serialization failure, the shape `pass.go` refuses explicitly with `errUnitRanTwice` — commits
the switch on the first run and answers `ErrConflict` on the second, because the row now holds
`to`:

```
a unit that ran the body twice answered … conflict: "orders" holds 2 and this names 1
(ErrConflict=true); the read target holds 2 after 2 activations
```

The operator reads "another operator won" for their own win. It is item 46's shape one door
over: `Split` is idempotent under a twice-running unit by construction and says so in its
comment; `Cutover` neither is nor says it is not.

### 55. `inTheCallersUnit` is a second spelling of `inACallersTransaction`  `[medium]`

Raised by the S4 review. `generation.go:360-369` and `topology.go:237-246` ask the same two
questions — `Tracker.Transaction(ctx)` answered, `authority.Valid()` — in the same order, in the
same Go package, and differ only in the clause the message ends with. The plan states the reason
(`topology.go` is outside S4's manifest fence and its message is about a handoff), and the reason
is honest, but the result is one rule in two places kept in sync by nobody: a change to what a
valid authority means has to find both. The shape that would close it is one helper taking the
clause, which needs `topology.go` in the section's fence.

### 56. The two new event decisions are numbered over the otel and i18n records  `[medium]`

Raised by the S4 review. `EVENTSOURCE_P4_PLAN.md:95,2969,3090,3099,3246` reserves `D-134` and
`D-135` for "a partition is a mask, and a topology change is a handoff" and "an effect is a
separate capability", and `docs/ai/decisions/` already holds
`D-134-one-opentelemetry-module-the-application-owns-the-sdk.md` and
`D-135-one-optional-i18n-module-owns-deterministic-presentation.md`, both merged with
`fefa3e9`/`939bcd9`. The plan and the usecases already *cite* the wrong ones —
`EVENTSOURCE_P4_PLAN.md:265` argues that a bug would happen "while D-135 claims the boundary is
closed", which is the i18n record. S6 writes those decisions and has to renumber first.

### 57. `cutting` joins two sentinels into one error  `[low]`

Raised by the S4 review. `generation.go:292-324` collects a cutover's spec refusals with
`errors.Join`, and they are not all the same class: `Checkpoints`, `Generations` and `Unit` are
`ErrSpec`, while `From == To` and the two covers are `ErrTopology`. A spec wrong in one of each
answers true to `errors.Is(err, ErrSpec)` and to `errors.Is(err, ErrTopology)` at once, so a
caller cannot branch on which door refused it. Both mean "this call cannot be made", which is why
this is low; the collection is otherwise the right shape and matches `New`'s.

### 58. Two structural tests still floor at sixteen files of `event/projection`  `[low]`

Raised by the S4 review. `scripts/projection_test.go:176` and `scripts/docs_test.go:1226` both
guard with `walked < 16` and both say "the package holds sixteen outside its tests". With
`generation.go` it holds seventeen, and S5 adds `effect.go`. The floors still catch a walk of the
wrong directory, so nothing is broken; what is wrong is the sentence a reader trusts, and a floor
that drifts downward is one that stops measuring.

### 59. The retiring half of item 52: a cutover away from a rowless generation is also `ErrRetired`  `[low]`

*Raised and half-answered while closing S4's GAP-1, 2026-09-12.* Item 52 is the arriving side — a
deployment whose log is empty has an arriving generation that drained perfectly and wrote no row,
and its first cutover is refused with a message about rows that are gone. The retiring side now
reads the same way, and deliberately: `Cutover` refuses a retiring cover no member of which holds a
checkpoint row, because the barrier folded from that silence is the origin and every arriving
generation clears it.

**What was decided rather than inherited:** the refusal has no override, matching the arriving arm,
and the message names *both* readings the rows cannot tell apart — a cover that is not the one this
generation records at, and a generation nothing ever recorded for. Standing a read target up where
nothing preceded it is a row the application's own `Generations` writes with its own fenced
`Activate`; it is not a switch `Cutover` derives, and no `CutoverSpec` field was added to admit one.
§UC-200 is the case.

**What is left, and it is item 52's wording problem exactly:** a deployment whose log is empty has
*both* generations rowless, so its very first cutover is refused twice over with two messages about
absent rows, neither of which says "there is nothing here yet". The refusal is safe and its text
names the readings; what it is not is the message that case deserves. Whatever answer item 52 gets,
this one takes the same shape.

### 60. `Effect.Envelopes` is a second clone of a page already cloned for the `Batch`  `[low]`

*Raised while writing S5, 2026-09-12.* `page.go:copyOf` exists because the projection re-reads what
it handed over — a retry re-applies the page it holds — so every attempt gets a fresh slice and a
fresh `bytes.Clone` of every payload. `gate.stage` applies the same rule to `Effect.Envelopes`,
which is right: the hand-off rule is the sink's too, and a sink that redacted a payload in place
would otherwise rewrite the page a retry re-applies. What it costs is a second clone of every
applied payload on every page of a projection that carries a sink, and nothing measures it. S6's
§6.14 records a read count against a one-projection baseline; the byte cost of the capability is the
same kind of question and has no number. Either it is measured there or the module page says a sink
doubles the per-page payload allocation.

### 61. The isolation pass stages under the attempt it was raised to, not the attempt the page was read at  `[low]`

*Raised while writing S5, 2026-09-12.* `Effect.Attempt` is `this.attempt`, and `applyFailed`
increments that before handing the page to `sequenceBySequence`. So the envelopes a page applied
whole report attempt 1 and the envelopes the same page applied one at a time after a sibling failed
permanently report attempt 2, for a sink that saw no failure of its own. `Batch.Attempt` has carried
exactly this meaning since phase 1 and the two agree, which is the argument for leaving it; what is
missing is a sentence saying `Effect.Attempt` is the *delivery's* attempt and never the envelope's,
so a sink keying an idempotency token on it does not key two tokens for one envelope.

### 62. `Spec.Effects` is inert beside `AfterApply` by refusal, where `Spec.Park` is inert by a line of code  `[low]`

*Raised while writing S5, 2026-09-12.* Refusal 11 makes `Spec.Park` harmless beside `Halt` in
`withDefaults` — dropped in one place rather than tested at the three that reach for it — because a
composition root wires one spec builder for two policies. `Spec.Effects` takes the opposite route:
`New` refuses it beside `AfterApply` outright, so `withDefaults` drops nothing and `outsideAUnit`
holds no call to the gate. The refusal is the stronger of the two and this is not a defect. What is
worth writing down is that the two fields are inert for different reasons, so a reader who learns
the `Park` rule and generalises it will look for a `spec.Effects = nil` that is not there — and a
future mode that admits `Effects` outside a unit would have to add one.

### 63. The flow reverse index points `effect.go` at a flow that does not mention it, and FL-039 is already taken  `[medium]`

*Raised by S5's review, 2026-09-12.* `docs/ai/flows/Index.md` gained rows mapping
`event/projection/generation.go` and `event/projection/effect.go` to **FL-038**, whose body
("a settled cursor becomes a durable checkpoint") names neither file and was not touched:
`grep -n "effect.go\|Effects\|Stage" docs/ai/flows/FL-038-*.md` answers nothing about this section.
CLAUDE.md's rule is that the reverse index and the flow's own file table move together, and that an
index an agent trusts and then finds empty is worse than a missing row. Beside it, S6's file list
promises `docs/ai/flows/FL-039-an-applied-envelope-becomes-a-staged-effect.md`, and **FL-039 is
already `FL-039-a-message-declaration-becomes-rendered-presentation.md`**, which arrived with the
i18n merge. S6 will either collide or renumber; deciding which before it writes the file is
thirty seconds and deciding after is a cross-reference sweep.

### 64. `Spec.Classifier` now classifies the sink's failures too, and nothing says so  `[medium]`

*Raised by S5's review, 2026-09-12.* `applyFailed` reaches `this.permanent(err)` for an `unstaged`,
so the application's `Classifier` — documented and taught as the verdict on **the handler's** error
(`docs/modules/en/projection.md:310`, "the handler's own error | `Apply` | `Classifier` decides") —
now also decides whether a refusal from `Effects.Stage` halts the projection or is redelivered. The
behaviour is the right one and `TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked` pins both
arms; what is missing is the row in the failure table and the sentence on the field, so a consumer
whose classifier was written against payload errors knows it is now answering a second question.
`Spec.Attempts` reaches the same error through `this.attempt >= this.spec.Attempts`, so a sink that
is away long enough halts a projection on the handler's budget.

### 65. `Effects` beside `Destination: Unchecked` is accepted and the consequence is not stated anywhere shipped  `[medium]`

*Raised by S5's review, 2026-09-12.* `refusedEffects` refuses `Effects` beside `AfterApply` and
beside a barrier-with-a-queue, and says nothing about `Unchecked`. [SPEC] §1.6 part 2 accepts that
deliberately and then owes a sentence: *"under `Unchecked` it is not [one database], and the
application's read path must resolve a generation from a database it is not reading from. Stated,
not hidden."* No shipped comment states it — `Spec.Effects`, `Spec.Generations` and `doc.go` all
describe the ownership row as if it were always in the unit that commits the advance. Under
`Unchecked` the stage and the advance commit together and the read model does not, which is
`AfterApply`'s window moved one resource over, and the module page is where it belongs (S6).

### 66. `EffectsFunc` is exported and exercised by nothing  `[low]`

*Raised by S5's review, 2026-09-12.* `grep -rn EffectsFunc --include=*.go .` answers three lines,
all of them its own declaration: no test, no example, no caller. It is in the plan's *Realises* and
it mirrors `HandlerFunc`, so it is the right surface to have; it is also a published adapter that no
arm compiles against, and S6 regenerates `docs/api/surface.md` with it on the page. One use in the
`_examples/` effect sketch, or one arm staging through it, is the whole of the fix.

### 67. S5's checkpoint transcript omits the failing line of the command it quotes  `[low]`

*Raised by S5's review, 2026-09-12.* The pasted block shows the four `ok` lines of
`go test -race -count=1 ./event/... ./scripts/` and not the `FAIL	github.com/frostgrove/vv/scripts`
line the same command printed, which is also why the `&&` chain as written cannot have reached
`event-kernel-moved`. The prose two paragraphs below names both baseline reds exactly and explains
that the chain was run out of order, so this is a filtered transcript beside an honest paragraph
rather than a concealment — and it is exactly the shape phase 1 failed on, so the convention is
worth fixing once: paste what the command printed, including the red, and let the paragraph explain
it.

### 68. A failure message claims a transactional read the fake cannot show  `[low]`

*Raised by S5's review, 2026-09-12.* `effect_test.go:719` reports *"the retiring projection staged
%v after the row moved, and the read happens inside the transaction that commits its own advance"*,
but the `ownership` fake (`generation_test.go:41-46`) ignores its context entirely and answers a map
under a mutex. What the arm actually shows is that the row is re-read per delivery rather than
cached — which is worth showing — and the transactional half is §UC-173's, live, in S6. The message
should say the half it proves; a message that claims the other half is how a reader concludes the
live case is redundant.

### 69. The N x M walk measurement, as a number  `[measurement — not a defect]`

*Recorded by S6, 2026-09-12, because [SPEC] §8.5 asks for evidence rather than an intuition.*
`TestEightWalksCostEightTimesOneProjectionsReads` (`event/eventpg/cost_integration_test.go`) runs
two generations at four partitions each over one log of 40 events, against a one-projection
baseline, with every runner's `ReadAll` counted on its own wrapper. Against PostgreSQL 17.9 at
`MaxRead: 8`:

```
N x M walks over one log: 8 runners (2 generations x 4 partitions) read 320 envelopes in 65 walks,
against a one-projection baseline of 40 envelopes in 12 walks — 8.0x the read traffic
```

**8.0x, exactly**, and every one of the eight reads the whole log — which is what "independent"
means here and is the half a ratio alone does not say. The walk count (65 against 12) is not 8x
because it also counts the empty polls a following runner issues, and those are `Idle`'s rather
than the topology's.

This is the number Reject 3's eventual shared-reader decision is made on. Nothing in this phase
acts on it: adding a filter to `Log.ReadAll` would make the log's contract about a consumer's
partitioning, and a shared reader is a second piece of coordination between runners that are
separate processes on purpose. What changes the calculation is a deployment that reaches a
partition count where the read traffic, rather than the handler, is the binding cost — and this
line is what that deployment will be compared against.

### 70. There is no `parktest`-style conformance harness for a `Park`/`Redriver`  `[medium]` — **CLOSED for `Park` by the conformance round, 2026-09-12**

*Raised by S6, 2026-09-12.* A `Checkpoints` implementation is proved by `eventtest.RunCheckpoints`
and its fourteen sections; a `Park` is proved by whatever its author happened to write. The
contract is not small — four methods with three different units of work between them, a
two-dimensional bound, a claim that must travel on every write it authorises, an eviction that must
be ordered against the loop's own blocking test — and the two count bounds are exercised only by
§UC-151's fixture while the byte bound is exercised only by the example ([SPEC] §8.3). What would
close it is a published suite with the same anti-vacuity rules `RunCheckpoints` has: a defect per
section, and a section nobody could run reported as `not certified` rather than as a pass. It is
not in this phase because the shape of the suite is a decision of its own — in particular whether
the harness may write letters directly, which is the only way to reach a bound without driving a
projection to it first.

**Closed for the `Park` half, 2026-09-12.** `eventtest.RunPark` writes letters directly — that was
the decision — and reports `outside a unit`, `inside the unit`, `committed state`, `counts`,
`identity` and `bounds`, one per tier the four methods run at. The two count bounds are driven
through `ParkFactory.Sequences`/`Letters`, declared by the implementation and reported *not
certified* when it declares neither, and the byte bound is still the example's alone. `Redriver`
and `Claim` are not certified by it.

### 71. A second store's topology certification  `[low]`

*Raised by S6, 2026-09-12.* The two `eventtest` sections a split rests on — `topology` and
`topology handoff` — are proved against `eventmemory` and `eventpg`, which are the two this
repository ships. A third implementation is now *provable* and none exists, so what the sections
actually discriminate is unmeasured outside the two stores they were written beside. The honest
scope of the claim is "these two pass", and the page says a phase-3-certified store may go red on
the third — which is exactly the sentence a third implementation would falsify or confirm.

### 72. S6's gate command does not set `FROSTGROVE_EVENTPG_TEST_PSQL`, so every psql cross-check is silently not taken  `[medium]` — **raised by the S6 review**

*Raised 2026-09-12.* `event/eventpg/projectioncase_integration_test.go:159-176` reads the psql
command out of `FROSTGROVE_EVENTPG_TEST_PSQL` and answers `(nil, false)` when it is unset; every
one of the six call sites — `destination.agreesWithPsql`, `livePark.letters`,
`liveGenerations.recorded`, `splitRows`, and the two checkpoint-row reads in
`rebuild_integration_test.go:240` and `projection_integration_test.go:1428` — then skips. The
plan's checkpoint block (`EVENTSOURCE_P4_PLAN.md:3611-3630`) exports only
`FROSTGROVE_EVENTPG_TEST_DSN`; the transcript beneath it (line 3683) exports the psql command as
well. So the section's evidence was taken with the cross-check on and the **command a future
re-run would follow takes it with the cross-check off**, while five of the nineteen §6 items and
the section header (*"rows are checked in the database with `psql` rather than in Go"*) rest on it.

It is not a wrong result: the review re-ran the whole tagged suite with the variable set
(`ok 119.168s`) and proved the check is not vacuous by pointing it at a broken command
(`-U nobody -d nowhere`), which fails `TestASplitWithNoParentRowWritesNothing` on three subtests
rather than skipping. What is open is that the DSN fails closed when unset by design and the psql
command fails **open**, and the two are set by the same gate.

The repair is one of two shapes and both are cheap: put the export in the checkpoint block beside
the DSN, or make `psqlAnswers` refuse when the DSN is set and the psql command is not — the same
rule the DSN already carries, applied one variable along.

### 73. `TestNoModulusIsAppliedToASequenceHash` watches functions by a name regex, so a second hashing function under another name escapes it  `[low]` — **raised by the S6 review**

*Raised 2026-09-12.* `scripts/projection_test.go:1103-1116` collects the functions to watch with
`regexp.MustCompile("(?i)hash")` over `checked.info.Defs`. A *total* rename of `hash` fails closed —
`len(hashes) == 0` is a `t.Fatal` — so the walk cannot silently resolve nothing. What escapes it is
an **addition**: a second key-derivation function named `fold`, `digest32` or `bucketOf` beside the
existing `hash`, with a `%` applied to it. The guard still sees one hashing function, so it does
not fire, and the modulus goes unreported.

The comment above the test (`projection_test.go:1067-1069`) reads *"The walk is over the operator
applied to a CALL of the package's own hash rather than over the word, so a rename is invisible to
it"* — which is true of the *operator* half and not of the *selection* half, and the two sentences
are one paragraph apart. [[D-140]] leans on this walk by name as the thing that stops
`Partition.Count()`'s published arithmetic being used, so the narrowing is worth stating.

The shape that would close it is selecting on the return type and the parameter shape
(`func(string) uint32` declared in this package) rather than on the identifier, or asserting the
set of such functions is exactly one and naming it.

### 74. The modulus "control" in the live partition case is a Go-side simulation, not a control over the positive arm  `[low]` — **raised by the S6 review**

*Raised 2026-09-12.* `event/eventpg/partition_integration_test.go:176-215` runs `modulusWalk` — a
hand-written `hash % N` walk over a synthetic log, with `budget := partition + 2` chosen so the
four cursors end at four different positions — and asserts that re-partitioning to `N+1` reorders
some keys and skips others. It exercises no vv code and touches no database.

That is a demonstration of the *argument* [[D-140]] makes, and a good one; what it is not is a
control in the sense `test/integration/gate_relscope_test.go` established, which is "assert the
leak **is** there without the mechanism, so a green positive arm is known to be proving
something". The positive arm's teeth come from somewhere else: `Partition.Matches` → `return true`
makes `TestFourPartitionsOverOneLogAndTheModulusControl` fail with *"the four partitions between
them applied the whole log did not happen"*, reproduced by the S6 review. So the test is sound and
the word "control" in its name and in D-140's **Proven by** table is doing work it has not earned.

Either rename the subtest to say what it is — the arithmetic the mask replaced, simulated — or
make it a real control by running the four partitions over a `Cover` built with a deliberately
overlapping member and asserting the double-apply the checked set prevents.

### 75. The split-under-a-running-parent re-delivery window, as a number  `[measurement — not a defect]`

*Recorded by the S6 review, 2026-09-12, because `## P4` item 6 leaves the window's size open and
calls it "a documentation question".* Driven against PostgreSQL 17.9 at `MaxRead: 4`: a log of 240
envelopes over 120 keys, one runner at `Whole()`, the `Split` committed while that runner is
mid-walk rather than after a drain.

```
the parent halted after applying 60 of 240 rows
240 distinct payloads over a log of 240; 8 were applied more than once
REVIEW: split-under-a-running-parent re-delivery window = 8 of 240 envelopes applied twice
```

**Nothing was dropped** — every payload of the log reached the read model — and the overlap is the
envelopes the parent applied between the cursor the split copied and the pass at which its next
save found no row. The size is therefore one page plus whatever the parent got through before the
save, not a function of the log's length, and it is at-least-once inside the delivery contract the
module page already states. `_examples/event-partitions/main.go:121-125` teaches draining the
parent first, which is what makes the window zero, and the module page's §7 does not put a number
on it. This line is the number.

### 76. `EVENTSOURCE_P4_PLAN.md` contradicts itself on `checkpointDefects`, and one of the wrong numbers is inside a ticked deliverable  `[medium]`

*Recorded by the P4 verification gate, 2026-09-12.* The shipped value is **14**
(`event/eventtest/checkpoints_test.go:277`), and `TestTheDefectInventoryIsTheSizeItSaysItIs`
and `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` both pass against it. The plan
carries the right number at line 1601 and its correction note at 1611, and the **wrong** one in
two places: line 573 (*"`checkpointDefects` 5 → 8"*) and line 3851, which is a `- [x]`
deliverable item reading *"`checkpointDefects` is 8 and every checkpoint section is named by
one"*. PLAN GAP-3's closure note repeats *"5 → 8"* as well.

---

## P5 — the wait, the receipt, the bounded read and the snapshot contract

Recorded under the 2026-09-08 policy: `[medium]` and `[low]` are scheduled here and left alone.
Everything below was raised by the phase-5 use-case audit (Round 1,
[`EVENTSOURCE_P5_USECASES_GAPS.md`](EVENTSOURCE_P5_USECASES_GAPS.md)) against
[`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md). The eleven blocking
findings (one `[critical]`, ten `[high]`) are in that file and are **not** repeated here.

### 1. The appendix's «Deadline возвращает timeout/**degraded**» has no verdict  `[medium]`

ES-05 part 3 names two deadline answers and [SPEC] §1.1 ships one: `ErrNotVisible` wrapping
`context.DeadlineExceeded`, plus `Visibility.Moved` as the discriminator. §8 tension 2 already
concedes what `Moved` is — *"a heuristic dressed as a field … a projector between two slow passes
has not moved either"* — so the degraded half of the appendix's sentence is answered by a field the
spec itself says is not the thing it is read as. `projection.State`'s `PhaseHalted`/`PhaseDegraded`/
`PhaseBlocked` are published values (`event/projection/state.go`) and the study names them as *"what
Marten issue #3912 is asking for"*; they are in-process and a waiter is usually not that process,
which is the real reason they are unavailable and is nowhere written down. Owed: one paragraph
saying why a wait cannot report a phase, or a second verdict that does.

### 2. `Repo.Digest`'s result is never tied to what `Repo.Append` writes  `[medium]`

[SPEC] §INV-116 says the digest is *"computed by `Repo.Digest` from the bytes that would be
written"*, which is true of the call and not of the caller: nothing checks that the `changes`
handed to `Digest` are the ones handed to `Append`, and the framework structurally cannot. A caller
that re-runs `decide(state)` between the two — an ordinary refactor — records a fingerprint for a
batch it did not write, after which its own retries never match. §INV-111 is the document's own
shape for an obligation the framework cannot verify (the sequencer a wait names); the digest needs
the same sentence, in the same place, and a use case that drives it wrong.

### 3. `Mark`'s contents and rendering are unspecified  `[medium]`

Beyond the blocking asymmetry (GAP-7), two smaller holes in the same type: nothing says what
`Mark` renders under `%v` or `String()` — UC-032 §11's no-data rule and §INV-124's
*"`event.Position` values appear in no message"* both bear on it — and nothing says whether two
marks are comparable or whether a `Mark` may be held across a process. `Barrier` has the same
questions answered by being three exported fields; `Mark` is opaque and answers none.

### 4. `ResolveSpec.Store` has no stated purpose  `[medium]`

§5.3's `ResolveSpec{Ledger, Store, Key, Issued}` carries a store that the `Resolve` doc comment
never mentions and the prose never uses; the only trace of what it is for is §UC-221's control
(*"zero calls to the event store other than the transaction question"*), which implies a check no
contract states. Either the field earns a sentence (which is GAP-4's close criterion) or it is not
in the spec.

### 5. §INV-110 and §INV-112 state call orders rather than falsifiable properties  `[medium]`

*"Each poll asks `Park.Sequences` first and, when that count is non-zero, `Park.Holds` …"* and
*"Each poll builds a fresh `event.Track` per cover member and calls `Load`; no tracker is retained
between polls"* are procedures. Both have real properties underneath — a parked sequence is named
within one poll rather than at the deadline; a wait never moves a fence — and both spell the
procedure rather than the property, which makes them true of one implementation rather than of any.
The falsifiers listed are behavioural, so nothing is unproved; what is missing is the statement a
second implementation could be held to.

### 6. ES-09: the `SnapshotFilter` composition rule has no answer in D-145  `[medium]`

Study nuance ES-09/3: Axon's javadoc rule 3 (*"return `true` if the snapshot data does **not**
correspond to the desired aggregate"*) exists because `combine` is an AND across every registered
filter, so a filter that returns `false` for anything it does not recognise *"silently turns off
snapshotting for the whole application"*. [SPEC] §1.4 specifies five column comparisons and no
composition, which is probably why the hazard does not apply — but D-145 is the contract a later
phase implements from, and the moment it grows a per-aggregate policy assembled from parts it
inherits the hazard. One sentence, recorded with the source, is the cost of not rediscovering it.

### 7. ES-09: «оператор может отбросить snapshot» has no invariant  `[medium]`

ES-09 part 5: *«обычные команды не зависят от наличия snapshot, оператор может его отбросить»*.
§1.4 gets most of the way there (the fallback, "history is never deleted", a snapshot is
replaceable) and never states the property in the form a test could take: **deleting every row of
the snapshot table changes no result anywhere**, and a command path that fails when a snapshot is
absent is a defect. §INV-121 and §INV-122 cover binding and fallback and not this.

### 8. ES-09: "a snapshot decode that panics falls back" assumes a `recover` nothing authorises  `[medium]`

§1.4, transferring Axon's `catch (Exception | LinkageError e)`: *"A snapshot decode that panics
falls back; a snapshot decode that returns a plausible zero value does not and cannot."* A recover
on the kernel's load path is a design decision of its own — `event/projection`'s existing recover
is scoped to a handler call and was argued — and D-145 asserting one in passing is the kind of
clause the implementing phase will read as settled. Either it is argued where it is stated, or the
sentence says that a panicking codec is the caller's defect and is not caught.

### 9. `Visibility.Behind` is an `event.Position` used as a distance, again  `[medium]`

`## P4` item 1 raised exactly this for `Readiness.Behind` and it is still open; §5.2's
`Visibility{… Behind event.Position …}` adds a second field of the same shape with the same
doc-comment caveat. Whatever answer item 1 gets — a distinct type, a `uint64` named for what it is,
or a module-page sentence about burnt positions — now has two fields to apply to, and the phase
that fixes one should fix both.

### 10. A wait against a generation with no checkpoint rows at all is unstated  `[low]`

The published `Observe` contract answers the origin with a nil error when **all** cover members are
fresh, and `ErrTopology` when some are and some are not. So a wait against a generation that has
never saved — a rebuild that has just been declared — polls against position zero and waits, which
is the right behaviour and is derivable only by reading `Observe`'s contract. §1.1 states the two
refusals `surveyed` buys and not the third answer it gives.

### 11. §0 says "moves three kernel names" and lists four  `[low]`

*"Phase 5 moves three kernel names and widens one application contract. `event` gains
`Repo.Digest`, `Repo.StateAt` and `ErrVersion` (§5.1); `event/projection` gains the wait (§5.2);
`event/receipt` is a new package under `event/` (§5.3). All four …"* — the sentence counts three and
the list has four, and §7.1's table has five rows. The number is load-bearing nowhere; the paragraph
is the first thing a reviewer reads about the phase's surface.

The code is right. What is wrong is the record a future reader checks to find out whether the
deliverable was met, and a ticked box with a wrong number in it is the one place a wrong number
costs the most. Fix the three statements to 14, or strike the number from the deliverable and
leave the test as the answer.

### 77. The alignment seam `EVENTSOURCE_P4_PLAN.md` names does not exist under that name  `[low]`

*Recorded by the P4 verification gate, 2026-09-12.* The plan at line 1893 and PLAN GAP-11's
closure both say *"S1 adds **one** seam to `harness_test.go` and everything after it uses that
one: `aligned(t)`"*. There is no `func aligned` in the repository. What shipped is the type
`alignedCheckpoints` (`event/projection/harness_test.go:211-222`), whose `Transaction` answers
`event.NewAuthority(backing, chosen)`, plus `(*stand).inUnit` (`:233`), which binds the
destination's executor over the same chosen value — which is exactly the seam the paragraph
describes, under two names instead of one.

The substance landed and the rest of that closure holds: the plan does say *"the alignment in an
untagged test is fabricated"* in full at 1901-1909, §INV-091's matrix row names **S6** alone for
the commit-together half, and `TestTheBlockingTestAndTheAdvanceAreOneCommit` exists in
`event/eventpg/park_integration_test.go`. Only the symbol name in the plan is wrong. Rename the
plan's prose to the two names that exist.

### 12. A third `< 18` file floor in `scripts/projection_test.go` that P-4 does not name  `[medium]`

*Raised by the phase-5 plan audit (Round 1, [`EVENTSOURCE_P5_PLAN_GAPS.md`](EVENTSOURCE_P5_PLAN_GAPS.md)), 2026-09-12.*

`EVENTSOURCE_P5_PLAN.md` **P-4** says *"the **two** file-count guards in `scripts/projection_test.go`
move with the package"* and names `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`
(`:141`, `files < 18`) and `TestNothingInTheProjectionPackageOpensATransaction` (`:178`,
`walked < 18`). There is a third: `TestNoModulusIsAppliedToASequenceHash` (`:1070-1073`) asserts
`len(checked.files) < 18` with the same message, *"the package holds eighteen outside its tests"*.

All three are floors, so none of them breaks when the package grows to twenty — which is exactly the
failure a floor exists to catch. Two of them move and the third silently stops measuring two
tenths of the package. Move it with the other two, or record why one of three is different.

### 13. The conformance table states `checkpointDefects` at 8 and it is 14  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`EVENTSOURCE_P5_PLAN.md` § *The conformance extension* holds the claim *"the suite's inventory did
not move"* in an evidence row reading *"`event/eventtest`'s own counts (`inventoried` at 29,
`checkpointDefects` at 8, the certified-section assertions)"*. `inventoried` is 29
(`event/eventtest/defects_test.go:47`). `checkpointDefects` is **14**
(`event/eventtest/checkpoints_test.go:277`), and fourteen is also the number of checkpoint sections
the same file asserts (*"certified %d of fourteen"*).

The gate itself is unharmed — `go test ./event/eventtest/` asserts both counts internally, whatever
the plan's prose says — but the number a section report is written from should be one that exists in
the tree. Correct it to 14, or drop the numbers and let the suite be the answer.

### 14. `WaitSpec.Committed` takes any `event.Store` and compares no backing  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`func (this WaitSpec) Committed(ctx context.Context, store event.Store, commit event.Commit) (Mark, error)`
carries six refusals and none of them is *"this is not the store the commit was written to"*.
`Repo.Append` treats the same question as load-bearing — step 5 of six, `backing.Equal(at.backing)`
→ `ErrWrongStore` (`event/repo.go:89-92`) — because a token minted over one backing must not be
spent on another.

A deployment with two backings that passes the wrong store gets a mark read out of the wrong
database at the same stream and version, and `Wait` then clears it against the right one's
checkpoint rows. The value exists to make the check: `Commit.Authority()` (`event/token.go:47`) and
`Authority.Same` (`event/authority.go:36`) are both exported.

What makes this a scheduling item rather than a quick fix: `Authority.backing` is unexported and has
no accessor, so `event/projection` cannot ask the question without an addition to `event` — which
phase 5 permits only in S1, and S1's checkpoint asserts the `event` section of
`docs/api/surface.md` grew by **exactly three** lines. Closing this after S1 costs a second kernel
move and a rewritten fence. Whoever opens it should open it at the top of a section list, not in the
middle of one.

### 15. P-7's rebuild-wait cost omits `surveyed`'s `ErrTopology` window  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

**P-7** states the poll cost for *"a generation that has not saved yet — a rebuild just declared,
which is the wait a deployment tool makes"* as two reads per member per poll, *"and the cost falls
to one per member as each member records its first checkpoint"*, and puts
`1 + (1 or 2) × |cover|` on the module page.

There is a third state between the two, and it is not a cost: `surveyed`
(`event/projection/generation.go:189-192`) refuses with `ErrTopology` whenever
`held.recorded > 0 && held.recorded < over.Count()`. A four-partition rebuild whose members record
their first checkpoints one at a time is in that state for the whole window. A wait that started
while all four were fresh polls through it (a later refusal is not terminal) and eventually reaches;
a wait that **starts** inside the window is refused terminally on poll 1.

The behaviour is right and §UC-244 covers its shape — *"a cover that is mid-split — some members
holding rows and some not"* — but the page as planned describes a steady state that a multi-partition
rebuild wait does not stay in. Either the page names the window, or it says the arithmetic applies to
a cover whose members have all recorded or none has.

### 16. S1's and S2's kernel-fence allowed sets exclude the fixtures their tests need  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`event-kernel-moved` fails a section on **any** path under `event/` outside its anchored allowed
set, and both no-database sections draw theirs tightly:

- S1 allows `^event/(repo|errors|token|repo_test|replay_test|refusalmessages_test|stateat_test|digest_test)\.go$`
  and the two `eventmemory` comment files. Its tests call for *"a recording store"* with a
  `StreamPage` of 4, *"a defect store answering `[v3, v1, v2]`"* and a payload over `MaxPayload` —
  and `event/recordingstore_test.go` and `event/fixtures_test.go` both exist and are **not** in the
  set.
- S2 allows `^event/projection/(mark|wait|errors|park|doc|mark_test|wait_test|harness_test)\.go$`.
  Its tests call for a recording `Park`, a movable `Generations`, a failable `Checkpoints` and a
  recording `Ticks`; `event/projection/park_test.go` and `generation_test.go` are not in the set.

The fence is doing its job — it fails loudly rather than absorbing the edit — but a section whose
first honest test edit turns its own gate red is a section that will be tempted to widen the ERE
mid-flight, which is the one move the fence exists to make visible. Either the allowed sets name the
fixture files up front, or the plan states that a fixture edit is a finding and each section reports
the path it had to add.

### 17. `_examples/event-receipts` has no named store, and `_examples` requires no `event/eventpg`  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

S6 publishes `_examples/event-receipts` as the reference `Ledger` — *"the one-statement claim with
`statement_timestamp()`, the completing `UPDATE`, a SQL-side `Horizon` from a configured retention,
a `jobs` periodic that sweeps"* — which is PostgreSQL SQL, and `make examples`
(`scripts/modules.sh:53-55`) runs `GOWORK=off go build ./... && vet ./... && test ./...` inside
`_examples` with no database.

`_examples/go.mod` requires no `github.com/frostgrove/vv/event/eventpg`, and the two shipped event
examples reach only `event`, `event/eventmemory` and `event/projection`. So the example either runs
its ledger over `eventmemory` with the SQL as a string constant — which is fine, and is what
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares — or it takes a new require and a matching
`replace`, which is the satellite trap `CLAUDE.md` names by hand. The plan says which SQL the
example carries and not which store it runs. Say it, and if a require lands, say the replace lands
with it.

### 18. `ErrUncommitted` joins a var block whose opening sentence says none of them crosses a store seam  `[medium]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`event/projection/errors.go:5-8` opens *"Eight, and none of them crosses a store seam: construction,
lifecycle, contention, routing, topology, the park's room, a redrive's grant and a generation's
rows. What a store refused travels as the sentinel `event` already publishes, so a consumer reads
one vocabulary rather than two."*

Phase 5 moves the count to twelve and adds `ErrUncommitted` — *"this store shows no event at the
version this commit reports"* — which is a statement about a store's visible state rather than a
projection's. [SPEC] §5.2's own gloss concedes as much (*"it is `receipt.Unresolved` one level down
and says so"*) and argues it is about the caller's transaction, not the store. The plan moves the
number and not the sentence. Decide which reading the file states, and state it.

### 19. P-3 names five walks riding on `checkedEventPackages` and there are seven  `[low]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`checkedEventPackages` (`scripts/projection_test.go:549`) is called from seven tests — lines 44, 62,
81, 101, 263, 710 and 1160 — plus `checkedProjection` (`:951`) on top. **P-3** names five, omitting
`TestNoConstructorTakesAProgressAndAnswersACursor` (`:60`) and the walk at `:81`. The fix P-3
prescribes (the floor `listed < 5` → `< 6`, plus the `slices.Contains` assertion) protects all of
them, so nothing is unprotected; the count is simply wrong in a paragraph whose whole job is to say
what would silently stop being asked.

### 20. `ByStream()` is `withDefaults`'s ninth clause, not its tenth  `[low]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

**P-1** calls `if spec.Sequence == nil { spec.Sequence = ByStream() }` *"its **tenth** clause"*. In
`event/projection/spec.go:349-384` it is the ninth of eleven: Park, Advance, Idle, `Backoff.Max`,
`Backoff.First`, the `Max < First` correction, Attempts, Tolerate, **Sequence**, Classifier, Ticks.
P-1's argument does not rest on the ordinal; the number is simply not the one in the file.

### 21. The snapshot gate merges D-132's two codec bands into one range  `[low]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

`EVENTSOURCE_P5_PLAN.md` § *The snapshot gate* reading 1 says D-132 *"recorded … **104.4–170.6 ms**
at 100 000"*, then observes *"Today's 100 000-event figure sits inside that band."* D-132's table
holds two separate rows for that length: **104.4 – 105.7 ms** with a no-op codec and
**163.4 – 170.6 ms** with `event.JSON`. Today's measurement, 108.4 – 111.3 ms, is on the no-op codec
and is therefore 3–7 % above the comparable number, not inside a band. The conclusion is unaffected —
the gate is refused on D-132's *"there is no consumer"*, not on the arithmetic — but a merged band
is a wider tolerance than the instrument has, and D-145 is planned to carry these numbers forward.

### 22. `replay`'s `upTo == 0` is an in-band sentinel while `StateAt` refuses version zero  `[low]`

*Raised by the phase-5 plan audit (Round 1), 2026-09-12.*

The planned loop is `replay(ctx, stream, upTo Version)` where *"zero means the end of the stream,
which is what a Load asks for"*, and `Load` calls `replay(ctx, stream, 0)`. Meanwhile `StateAt`
refuses version zero with `ErrVersion` (§UC-231), so one `Version(0)` means "no bound" one frame
down and "an impossible bound" one frame up.

`replay` is unexported and the two callers are three lines apart, so nothing is reachable today.
What it costs is the next reader: **P-13** already records that the phase which adds the snapshot
base state seeds this same loop, and a seed of zero will mean "from the origin" in a function where
zero already means "to the end". A named bound (`allVersions`, or a `*Version`) removes the question
before that phase has to answer it.

### 23. `jobspg`'s ambient placement cannot retry a lost race above READ COMMITTED  `[medium]`

*Raised while closing the phase-5 plan audit's GAP-5, 2026-09-12. A finding about `jobs`, not about
`event` — recorded here because this is where it was found and because D-142 adjudicates the
mechanism it belongs to.*

`jobspg.Driver.Place` enlists in the caller's ambient transaction when one is bound
(`jobs/jobspg/driver.go:22-42`) and routes through `TxStager.Stage`, which retries
`errIntentConflict` up to three times **on that same transaction** (`jobs/jobspg/stager.go:112-121`).
The retry is what turns a lost race into `EnqueueExistingSamePayload` rather than
`jobs.ErrConflict`: attempt one's `insertIntent` (`repo_ops.go:232-250`) finds the winner's row and
reports zero rows, and attempt two's `findIntent` reads it.

That works at READ COMMITTED, where each statement takes a fresh snapshot. It cannot work above it.
Measured on PostgreSQL 17.9 (`localhost:55432`, two sessions, the winner holding open 3 s): an
`INSERT … ON CONFLICT … DO NOTHING` that waits on a concurrently-committing conflicting tuple
raises **SQLSTATE `40001`** at REPEATABLE READ and SERIALIZABLE, and every statement after it in
that transaction answers *"current transaction is aborted, commands ignored until end of
transaction block"*. So on the ambient path at either stricter level, attempt one aborts the
**caller's** transaction, attempts two and three cannot run, and the caller receives
`jobs.RejectPlacement(jobs.ErrConflict)` for what is a plain repeat — with its own unit already
dead. The non-ambient path (`Driver.Place`'s own `BeginTx`, `driver.go:45-54`) is unaffected,
because each attempt gets a fresh transaction.

Nothing is written twice and nothing is lost; the cost is a misreported verdict and a retry loop
that is dead code above READ COMMITTED. The honest repair is for `Stage` to stop retrying on a
transaction it does not own and to surface the serialisation failure as retryable — `40001` is
already `errs.CodeSerializationFailure` (`errs/sqlerr/postgres.go:15`) — so the caller's own
`crud.InNewTx` retries the unit. That is a change to `jobs`, with its own tests in all three
`jobs` drivers, and it is not phase 5's.

### 24. `Digest` and `Append` carry the same four pre-store steps as two copies  `[medium]`

*Raised by the phase-5 S1 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S1_GAPS.md`](EVENTSOURCE_P5_S1_GAPS.md) GAP-3).*

`event/repo.go:113-132` (`Append`) and `event/repo.go:176-192` (`Digest`) are byte-identical but
for the return values — `checkKey`, the `decidedFor` loop, the `change.err` loop, `records` — and
what binds them is a sentence in a comment (`:154-158`, *"answers the same refusals Append would"*).
A fifth pre-store check added to `Append` leaves `Digest` answering a fingerprint for a batch
`Append` will refuse. **Still open after GAP-1 was closed:** all four existing steps now have a
table row and each row cross-checks `Append`'s own sentence, so the correspondence holds for the
steps that exist today — but it holds by enumeration, and the *fifth* check nobody has written yet
is what this item is about.

The extraction is available — `checkKey`, then `Append`'s empty-batch short circuit, then one
helper carrying steps 2–4 that both call. The cheaper half is a test that drives every refusal
`Append` has before the store and asserts `Digest` answers the same sentence, which is the shape
**P-16**'s `TestEveryRefusalClaimHasIsReachableThroughOnce` uses one section later for the same
kind of two-door correspondence.

### 25. `StateAt` reports a store that truncated a history as the caller's bad request  `[low]`

*Raised by the phase-5 S1 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S1_GAPS.md`](EVENTSOURCE_P5_S1_GAPS.md) GAP-4).*

`replay` documents its own blind spot (`event/repo.go:307-310`) and `event/eventtest` makes it a
defect. What S1 adds is the rendering. Driven: a store answering a 2-envelope page where it
publishes a `StreamPage` of 4, read at version 8 over an 11-event stream, answers `ErrVersion` —
*"this stream is shorter than the prefix this read was bounded at"* — which wraps
`crud.ErrBadRequest` (`event/errors.go:43`) and renders 400. A store defect therefore reaches the
caller as their own off-by-one. §INV-118 names three causes for `ErrVersion`; this is a fourth, and
a fourth indistinguishable cause is the shape the ES-08 study criticises Marten's `null` for.

`[low]` because the framework structurally cannot tell the two apart without the confirming read
the store contract deliberately refuses, and because `Load`'s answer to the same store is worse — a
silently truncated state with no refusal at all. Owed: one sentence on both `event.md` pages saying
`ErrVersion` also covers a store that broke its short-page clause, and that the conformance suite is
where that is caught.

### 26. `Repo.Digest` does not answer `ErrWrongStore`  `[low]`

*Raised by the phase-5 S1 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S1_GAPS.md`](EVENTSOURCE_P5_S1_GAPS.md) GAP-5).*

Driven: `Digest` over a token minted against another backing returns a fingerprint and a nil error,
while `Append` over the same token answers `ErrWrongStore` (`event/repo.go:133-136`, step 5). The
fingerprint is **identical** to the one taken over this store's own token, which is correct and
deliberate — §INV-116 excludes the backing from the preimage — so this is about the refusal and not
about the digest. The comment's *"a caller that digests first learns a malformed append before it
claims anything"* (`:156-157`) is true of four of `Append`'s six steps and not of the fifth.

Harm is bounded: a claim runs inside the caller's transaction, so the refused append rolls the claim
back with it. Owed is either the backing comparison inside `Digest` — which costs **P-12**'s
zero-store-call property one `Store.Backing()` call and therefore needs an argument — or one clause
narrowing the sentence to the four steps it enumerates.

### 27. The live `eventpg` suite is order-dependently red on the burnt-gap walk  `[low]`

*Observed while closing the phase-5 S1 review's two blocking findings, 2026-09-12. Not caused by
that work: the only non-test change was a comment, and `event/digest_test.go` is a `_test.go` of
package `event`, which is not compiled into `eventpg`'s test binary.*

`TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`, first subtest, failed once inside a full
`go test -race -count=1 -tags=integration ./event/eventpg/...`:

```
watermark_integration_test.go:310: the walk beyond the gap answered [], and a walk that stops for
good at a gap that can never be filled never finishes
```

Run alone it passes (`ok 1.219s`), and the two full runs after it were green
(`ok 118.035s`, `ok 114.169s`). **This is the already-recorded cluster-wide-`xmin` drawback showing
up as a test flake, not a second defect**: `event/eventpg/read.go:319` reads
`pg_snapshot_xmin(pg_current_snapshot())`, which is the oldest running transaction id in the whole
database, so any other session that has written and not yet ended holds the settlement floor down
and the walk does not pass the burnt gap. In a full suite run, the other tests are exactly such
sessions.

Owed: nothing new about the mechanism — that item already carries the mitigation to write down.
What is owed **here** is that the test has no isolation from the rest of the suite while asserting
a property that depends on the cluster being quiet, so it can be red for a reason that is not a
regression. Either it settles its own floor before asserting, or it says in its failure message
that a concurrent session anywhere in the cluster produces this exact output. `[low]` because the
behaviour under test is correct and documented; what is wrong is that a green suite is not
reproducible.

### 28. The flow-index walk's own file floor is two sections behind the package  `[low]`

*Observed while closing phase-5 S2, 2026-09-12, and not caused by it.*

`TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` (`scripts/docs_test.go:1398`) guards its
walk with `walked < 16` and the message *"the package holds sixteen outside its tests"*.
`event/projection` held **eighteen** before this section and holds **twenty** after it, so the floor
has been passing over a walk that could have found a quarter of the package missing. P-4 moved the
two guards in `scripts/projection_test.go` because the plan named them; this third one is in another
file, was not named, and is left alone under the delivery policy rather than fixed because it is a
one-line change.

Owed: the floor moves to the package's real count in the same change as the next file that lands
under `event/projection`, and the sentence stops naming a number a reader has to trust. It is the
same defect `event/projection/lifecycle_test.go:235`'s `walked < 9` carries (P4 backlog), so the two
are worth closing together.

### 29. §UC-243's own "Then" has no arm — only its control ships  `[medium]`

*Observed while reviewing phase-5 S2, 2026-09-12.*

§UC-243 requires two things: that `spec.Committed` mints a mark carrying the distinct keys `A` and
`B` in first-appearance order, **and** that `Wait` then *"answers `ErrParked` from the second
`Holds` call"*. `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` ships the use case's
**Control** — a park holding neither key, `Holds` counted twice per poll — and no arm in the
repository parks the mark's *second* key. The coverage matrix records UC-243 as proved by that
count, so the claim that a commit spanning three sequences is protected in all of them rests on a
call count rather than on an answer.

Driven during the review and the behaviour is correct: a commit `A,B,A` with the queue holding `B`
answers `ErrParked` with `Visibility{Parked:true, Polls:1}` and two `Holds` calls, both outside a
unit — and the same holds with `StreamPage` forced to 1 so the commit spans three pages. So this is
a coverage hole, not a defect.

Owed: one subtest beside the existing control, parking the second key and asserting `ErrParked`,
with the first-key arm kept so the pair shows the loop does not stop at index zero.

### 30. `WaitOf` admits a `Spec` whose `Checkpoints` is nil  `[medium]`

*Observed while reviewing phase-5 S2, 2026-09-12.*

`WaitOf` (`event/projection/wait.go:68`) refuses the zero `Cover` and a `Spec` that names no
identity, and derives `Checkpoints` from the same `Spec` — but does not refuse a nil one, where
`New` refuses it at construction. Driven: `WaitOf(Spec{Name:"orders", Generation:2}, cover)`
answers `err=<nil>`, and the refusal lands on the first poll as

```
projection: this projection cannot be assembled from this spec: "orders@2" is not a name a
checkpoint row can be keyed by, or Checkpoints names no store: … this call names none
```

reported as `Visibility{Polls: 1}` for a poll that issued no store call of any kind. The refusal is
prompt, typed and names no data, so nothing is wrong with what the caller is told — what is wrong
is where it is told, and that `Polls` counts a round trip nobody made.

Owed: the same `absent()` door every other field of a `WaitSpec` gets, with a row in
`TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong`'s fourth arm beside the zero-`Cover`
and no-identity ones.

### 31. INV-124's refusal table reaches 16 of the wait's 20 new error paths  `[medium]`

*Observed while reviewing phase-5 S2, 2026-09-12. Renumbered 2026-09-12 when P-19 added the
twentieth path and its row landed in the same change, so the four below are the same four.*

`TestNoRefusalOfAWaitNamesAPositionOrAKey` walks sixteen rows. The four new refusal paths it does
not reach are `WaitOf`'s two (zero `Cover`, no identity), `Committed`'s wrap of a
`store.Transaction(ctx)` error, and `resolved`'s zero-position `ErrUncommitted`
(`event/projection/wait.go:180-182`). All four were read by hand during the review and none renders
a key, a stream, a position, a version or a cursor, so INV-124 holds in fact — `WaitOf`'s
no-identity row wraps `NewIdentity`'s refusal, which names at most one separator character, and the
zero-position one names no number at all. What is missing is the arm that keeps it holding when one
of those messages is next edited.

Owed: four rows in the existing table. The `Transaction`-error row needs a store whose
`Transaction` refuses, which `watchedStore` can answer with one more field.

### 32. Two published doc sentences of §5.2 did not land  `[low]`

*Observed while reviewing phase-5 S2, 2026-09-12.*

`WaitOf`'s doc comment no longer carries §5.2's *"It applies `ByStream()` where `Spec.Sequence` is
nil, which is the default `New` applies and the one a hand-written spec forgets."* The shipped
comment explains the derivation through `withDefaults` and the `Park` clause, which is the more
interesting half, but a consumer reading GoDoc no longer learns the `Sequence` default at all —
and that default is one of the three obligations INV-111 says a hand-written spec carries.

`ErrUncommitted`'s doc drops §5.2's closing sentence *"It is `receipt.Unresolved` one level down and
says so."* That is understandable at S2 — `event/receipt` does not exist yet — and is owed back by
S3, which is when the forward reference becomes resolvable and when a consumer reading either
sentinel needs to know the two are the same fact at two levels.

Owed: one clause restored to `WaitOf`'s comment now; the `ErrUncommitted` sentence added in S3's
own change, beside `receipt.Unresolved`'s own doc.

### 33. A third copy of the nil-interface predicate  `[low]`

*Observed while implementing phase-5 S3, 2026-09-12.*

`event/receipt/claim.go`'s `absent` is a reflection-over-`Kind` nil check, and it is the **third**
spelling of one idea in this subtree: `event/projection/spec.go:396` has the same function under the
same name, and `event/nilByAnyRoute` is the kernel's own. The two under `event/` cannot share
one — `receipt` importing `projection` would make an optional consumer a dependency of a durable
record, and `event` exporting it would put a Go-language predicate on the vocabulary's published
surface.

What a third copy costs is what a third copy always costs: a fix to one of them is a fix to one of
them. The three agree today and that was checked by reading, which is the evidence a duplicate is
allowed to have exactly once.

Owed: either an `event/internal/...` home the three share — which is the first `internal` package
this subtree would have and is a decision, not a refactor — or a recorded note on each that the
other two exist. Not urgent: the predicate is ten lines with no branch a test can reach differently.

### 34. A collided `Claim` hands the caller the other operation's row  `[medium]`

*Raised by the phase-5 S3 code review, Round 1 — [`EVENTSOURCE_P5_S3_GAPS.md`](EVENTSOURCE_P5_S3_GAPS.md) GAP-1.*

`event/receipt/claim.go:115-118` seats the stranger's `Receipt` in the `Held` it returns beside
`ErrCollision`, and `Held.Receipt()` hands it back unfiltered. [SPEC] §5.3 says the collided
`Held` carries *"the verdict for a caller that wants to branch"* — the verdict, not the row.
Driven: a second operation under one key answered `held.Receipt()` carrying the first operation's
stream `"A-17"`, range `1..1`, fingerprint and `RecordedAt`. The refusal *message* names none of
it — `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` proves that — and the value channel
does, so INV-124's purpose is defeated through the door the error channel keeps shut. The stream
key is what that test's own comment calls *"a customer's identifier"*.

Not `[high]`: the sentinel and the message are correct and the leak needs a caller that branches
on the verdict and reads the receipt on `Collided` — which is the sanctioned shape one step wrong,
and compiles.

Owed: `Held.Receipt()` answers the zero `Receipt` on `Collided`, or §5.3 says in words that it
answers the stranger's row and why that is safe; either way a test asserts it with a `Repeated`
control, and the refusal-message test gains an arm that walks the returned `Held` and not only the
string.

### 35. `Held` is half-synchronised — `Receipt()` races `Complete` under `-race`  `[medium]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-2.*

`claimed.completed` is an `atomic.Bool`; `claimed.receipt` beside it is written unguarded at
`event/receipt/claim.go:194` and read unguarded at `:76`. Driven: `WARNING: DATA RACE`, write at
`claim.go:194` against a read at `claim.go:76`.

Not `[high]`: the blast radius is one `Held`, which belongs to one transaction, which
`database/sql` already forbids sharing between goroutines — so it is not the process-global shape
`CLAUDE.md`'s `-race` sentence is about. What keeps it above `[low]` is the `atomic.Bool`: a
reader is entitled to conclude a `Held` is safe for concurrent use, and it is safe for one of its
three methods.

Owed: one guard covering both, or a `Held` doc sentence saying it is single-goroutine and what the
atomic is for. Plus a concurrent `Complete`/`Receipt()` test that is green under `-race`.

### 36. `Complete` burns the `Held` before the ledger write succeeds  `[medium]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-3.*

`event/receipt/claim.go:183-195` runs the `CompareAndSwap` before `Ledger.Complete`, and nothing
resets it on failure. Driven with a ledger whose `Complete` fails once: the first call returns the
ledger's error, the second returns `ErrSpec` *"this claim has already been completed"* — and the
row was never written, so the durable state is `Incomplete` while the in-process answer says the
opposite, under the sentinel that blames the caller for a ledger fault.

Not `[high]`: inside PostgreSQL a failed statement poisons the caller's transaction and the whole
unit rolls back, which is what §1.2 relies on everywhere else. It matters for a `Ledger` over a
backend where one statement can fail recoverably.

Owed: move the CAS below the ledger write or reset it on failure, and one §5.3 sentence saying
which answer a second `Complete` after a failed one gets.

### 37. `Receipt.RecordedAt` is validated at neither door, and its absence disables the horizon check  `[medium]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-4.*

`event/receipt/resolve.go:182` guards the `ErrLedger` horizon comparison with
`!held.RecordedAt.IsZero()`, and `claimAnswered` (`claim.go:253-269`) never asks about
`RecordedAt` on the win path either — although `receipt.go:19-22` makes it the one field the
ledger is contractually required to fill. Driven: a complete row with a zero `RecordedAt` beside a
`Horizon` of **2099-01-01** resolved to `[standing found]`, `err=<nil>`.

The failure it lets through is P-17's named window: a ledger that omits `recorded_at` from its
`SELECT` publishes an optimistic horizon unchallenged, and a swept row then reads `Unresolved` for
ever instead of `Expired` — the monotone-growth failure §"Retention" exists to close. `ErrLedger`'s
own rule is *"a ledger is trusted exactly as far as its answers are"*, and this is the one answer
that is never checked.

Owed: `ErrLedger` for a zero `RecordedAt` at both doors (or a contract sentence permitting it), and
a distinct-sentence arm in `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused`.

### 38. The empty-commit exemption admits an empty `Commit` from another unit, and nothing writes it down  `[medium]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-5.*

`event/receipt/claim.go:180-190` skips the authority comparison on an empty `Commit`, by design
(`event/token.go:23-28`), leaving `Commit.Stream()` as the only discriminator. Driven: an empty
commit for `A-17` minted in an earlier, already-committed unit, handed to the completion of a claim
whose own append wrote one event to `A-17`, was **admitted**, and the durable row reads
`first=0 last=0 complete=true`. Every later retry is answered `Repeated` with an empty range and
reports *done, nothing changed* — verbatim §UC-222's **Must not**, reached through an admitted
`Complete` rather than through an unresolved claim.

Not `[high]`: it needs caller malpractice, and it is genuinely undecidable on the current surface —
an empty commit carries the invalid authority wherever it was minted, and `ClaimSpec` records no
version to compare `Commit.Last()` against. The finding is the silence, not the hole: §1.2's own
method for an undecidable hazard is to state it and pin it with an inverted control, applied to
§UC-250's third way and not here, while `Held.Complete`'s doc claims it *"says which check is
absent and why rather than leaving the gap to be found"*.

Owed: the sentence in §5.3 and on the module page beside UC-250's, plus a case in the
`gate_relscope_test.go` inverted shape. Closing it rather than stating it means `ClaimSpec` carries
the token's version, which is a surface decision.

### 39. The repeat path never compares the row's `Stream` with the claim's  `[low]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-6.*

`claimAnswered` returns `nil` for `!won` after checking the key alone (`claim.go:260-262`), and
`verdictOf` decides on the fingerprint alone. Driven with a ledger answering a complete row whose
fingerprint equals the claim's and whose stream is somebody else's: `[verdict repeated]`, no error,
`held.Receipt().Stream.Key == "SOMEBODY-ELSE"` against a claim for `"A-17"`.

Unreachable with a conformant `Ledger` — `digestOf` opens the preimage with the composed stream, so
a row for another stream cannot compare equal. It is an asymmetry in the `ErrLedger` door (the win
path checks the stream, the repeat path does not) and the study's nuance 8 — *the row records which
aggregate the key was spent on* — reproduced through the fingerprint rather than the column.

Owed: one `if` with its own sentence, or a comment naming `digestOf`'s first field as the reason
there is none.

### 40. `ErrIncomplete` is what a second `Claim` of one key inside one unit gets, and no page says so  `[low]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-7.*

Driven: two `Claim`s of one key in one transaction answer `[verdict recorded]` then
`ErrIncomplete`. The answer is correct — at that instant the staged row genuinely is incomplete —
but `ErrIncomplete`'s own text calls itself *"a defect report rather than an ordinary outcome"* and
sends the reader to `Resolve` and then to a human, which is the wrong advice for a caller that
simply wrote two `Once`s under one key.

Owed: a sentence on the module page naming the in-unit re-claim among `ErrIncomplete`'s causes,
with §INV-126's key-per-append recipe as the fix.

### 41. INV-123's fourth falsifier did not ship  `[low]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-8.*

The coverage matrix routes INV-123's checkpoint to S3 and names four falsifiers, the fourth being
*"a recording source asserting no `Begin`, `Commit` or `Rollback` on any path"*. No recording
`crud.Source` exists in `event/receipt`'s fixtures; `countedStore` counts `event.Store` calls and
`event.Store` has no `Begin`. What shipped is `scripts/projection_test.go:181-194` — a call walk,
not a spelling walk, over `../event/receipt` with a floor of seven files and a fixture control
reporting all six shapes — plus `startsNothing` and the dependency charge, all re-run green.

The property is held; the matrix promises a fourth arm that is not there.

Owed: either the recording-source arm, or the matrix row dropping the fourth falsifier and saying
the AST walk subsumes it.

### 42. The flows reverse index gained S2's two files and not S3's seven  `[low]`

*Raised by the phase-5 S3 code review, Round 1 — GAP-9.*

`docs/ai/flows/Index.md:620-625` adds `event/projection/mark.go` and
`event/projection/wait.go` and none of `event/receipt`'s seven non-test files. The plan routes
FL-043 and every doc obligation to S6, which is a defensible reason to wait — but S2 did not wait,
so the index is half current, and `CLAUDE.md`'s own sentence is *"an index that does not list a
file is worse than a missing file — an agent trusts the index and stops looking."*

Owed: S6's FL-043, and a row for each of the seven in the reverse index.

### 43. Both module pages still say `Holds` runs inside your unit of work, full stop  `[medium]`

*Raised while implementing phase-5 S4 — the live proof of ES-05.*

`docs/modules/en/projection.md:547-555` and its `ru` sibling carry *"**Where each method runs is
part of the contract, and the four differ.** `Park.Holds` and `Park.Park` are called **inside** your
unit of work"*, which is now half the contract: `event/projection/park.go` was widened in S2 with
*"HOLDS IS ALSO ASKED OUTSIDE A UNIT, BY A WAIT, AND AN IMPLEMENTATION MUST ANSWER THE COMMITTED
STATE THERE"*. A consumer implementing the page requires the ambient transaction in `Holds`, and
every `Wait` over a projection that parks then fails its **first** poll — which is terminal — with
that implementation's own refusal instead of `ErrParked`. S4 hit exactly this: the live suite's own
queue refused outside a unit and three cases failed for the fixture rather than for the code
(`event/eventpg/park_integration_test.go`, `livePark.reading`).

S6's module-page row says "the wait section on `projection.md`", which does not name this paragraph,
and the release-note row records the widening rather than the page. The placement sentence is
`en:548` / `ru:572`, and the fast-path paragraph beside it (`en:560`, `ru:584`) says `Holds` is
"never called" while the queue is empty — true of the loop and now also true of a wait, by the
`Sequences`-first gate, which is worth saying rather than leaving to be read as the loop's.

Owed: the `Where each method runs` paragraph on both pages naming the wait's door, and a doc check
in the shape of `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition` that fails when a page
says `Holds` runs inside a unit without the second clause.

### 44. UC-208's bound-transaction assertion never covers `Holds`  `[medium]`

*Source: S4 review round 1 (`EVENTSOURCE_P5_S4_GAPS.md` GAP-3).*

Both standings of `TestTheCallCountBudgetAndItsPlacement`
(`event/eventpg/wait_integration_test.go:779-892`) run over an **empty** queue — the second drains
it with `queue.clear` so that only `Quarantined` is non-zero — so `watched.counted(t, "Holds", 0)`
is asserted and the `if call.bound` loop only ever walks `Sequences` calls. [SPEC] §UC-208 says
*"Every recorded call carries no bound transaction, which is the placement **the widened `Holds`
sentence** and the unchanged `Sequences` one both promise"*, and the widened sentence is the half
with no witness. It is also the half S4's correction 1 had to change a live fixture for
(`livePark.reading`).

Driven in review and correct: a wait over a queue holding somebody else's sequence answered
`{Reached:true … Quarantined:1 Parked:false Polls:2}` with calls
`[Sequences bound:false, Holds bound:false, Sequences bound:false, Holds bound:false]`. So this is a
missing proof on a path `make api` and `check-event-kernel` both agree they cannot see — which is
[SPEC]'s own argument for why the recording park exists.

Owed: a standing with a **non-empty** queue holding a sequence that is not the mark's, asserting
`Holds` once per poll with `bound == false`, and that the wait still reaches. It belongs beside
item 43's module-page sentence about where each `Park` method runs.

### 45. The cross-spec `Mark` guard compares the projection name alone, and the reason covers only barrier marks  `[medium]`

*Source: S4 review round 1 (`EVENTSOURCE_P5_S4_GAPS.md` GAP-4).*

`event/projection/mark.go:22-24` — *"The comparison is on the projection name alone and never on the
generation, because a barrier of another generation of the same projection is the cutover case a
wait admits"* — argues the exemption for `MarkOf`, whose mark carries no sequence key. It does not
argue it for `WaitSpec.Committed`, whose mark carries *"one projection's sequencer's answers"* (the
same comment, two sentences earlier). A generation that re-keys its `Sequencer` — a legitimate
reason to run one — plus a mark minted from the other generation's `WaitSpec` gives
`Holds(ctx, orders@3, <gen-2 key>)` → false → `Reached: true` for a change generation 3 parked:
§UC-242's wrong-`Sequence` failure reached through the generation door.

The shape is in the section's own tests: `TestACutoverThatCommitsWhileAWaitIsRunning`'s two controls
mint under one spec and wait under another (`unscoped.Until = waiting.Until`, `:1355` and `:1406`),
safe only because both generations there share `ByStream()`.

Owed: `mark.go`'s paragraph distinguishing the two doors, or `Mark` recording what it needs to
refuse a cross-generation `Committed` mark; plus a test that mints under a differently-keyed
sequencer at another generation of the same name, with the same-sequencer control beside it.

### 46. The checkpoint rows the census rests on are read through `database/sql`, not through `psql`  `[medium]`

*Source: S4 review round 1 (`EVENTSOURCE_P5_S4_GAPS.md` GAP-5).*

`storedCheckpoint` / `maybeStoredCheckpoint`
(`event/eventpg/checkpoints_integration_test.go:525-549`) read on a second pool with a hand-written
`SELECT` and no `psqlAnswers` cross-check, unlike `destination.rows`, `livePark.letters` and
`liveGenerations.recorded`. [SPEC] §6's preamble is *"Rows are checked with `docker compose exec -T
postgres psql` … and not by trusting Go"*, and the rows S4 rests on most come from that helper:
*"the checkpoint row stands at or above the mark"* (`wait_integration_test.go:464`), the lagging
member (`:570`), `vis.At == row.highest` (`:750`, `:1188`) and `row.advance == saves` (`:962`).

The second-pool read is already independent — its own connection, its own statement, no store code —
so what `psql` adds is ruling out a driver-level artifact. The helper predates S4; the review's own
`psql` reconstruction of the parked pair agreed with it exactly
(`advance=1 highest=1 quarantined=1`, one letter, an empty read model).

Owed: `maybeStoredCheckpoint` cross-checking through `psqlAnswers` in the shape `livePark.letters`
already has, with the whole tagged suite green twice with it armed — or [SPEC] §6's sentence
narrowed to the rows it means.

### 47. The S4 record says `make check` is "fourteen checks"; it is eleven  `[low]`

*Source: S4 review round 1 (`EVENTSOURCE_P5_S4_GAPS.md` GAP-6).*

`EVENTSOURCE_P5_PLAN.md:2225-2226` says *"`make check` (fourteen checks, including
`check-event-kernel`)"*. Measured at HEAD, `make check 2>&1 | grep -cE "^check-[a-z-]+: ok"` answers
**11**: deps, tiers, utils, triplets, todo, replaces, tidy, otel-schema, otel-module, workspace,
event-kernel. S3's report counted *"eleven arms"* for the same command on the same tree. The gate is
green either way; the number in the record is wrong and this project treats a count as evidence.

### 48. `BenchmarkStateAt`, measured — the number ES-09's Gate 1 is compared against  *(a recording, not a finding)*

*Source: S5, per plan **P-14**. [[D-132]] refuses a measured **cost** as a reason to build a
snapshot, so this lands here and not in D-144 or D-145, where it would slowly become an argument it
was refused.*

PostgreSQL 17.9 at `localhost:55432`, Intel i9-10900K, a 120-byte payload, a no-op codec and a no-op
fold, `-benchtime 20x -count 3`, over the **same** 100 000-event stream `BenchmarkStreamReplay`
uses. Two runs of the checkpoint, an hour apart:

| Bound | events read | ns/op, run by run | ns/event |
|---|---|---|---|
| **1 %** | 1 000 | 1 236 968 · 1 194 960 · 1 186 684 — and 1 235 713 · 1 117 943 · 1 070 646 | 1 071 – 1 237 |
| **50 %** | 50 000 | 57 748 776 · 56 611 208 · 58 043 237 — and 65 425 826 · 68 269 076 · 71 209 222 | 1 132 – 1 424 |
| **100 %** | 100 000 | 115 303 982 · 113 422 527 · 106 297 019 — and 137 064 539 · 125 590 370 · 120 504 206 | 1 063 – 1 371 |

Three readings, and the third is the one that matters to a later phase.

1. **The bound costs what it reads and nothing else.** 1 % of the stream costs ~1 % of the full
   replay: the per-event cost is flat at 1.06 – 1.42 µs across a hundredfold range of bounds, which
   is the same neighbourhood `BenchmarkStreamReplay` reports for a full load at this length
   (108.4 – 111.3 ms in the plan's own measurement, 106.3 – 137.1 ms here). A bounded read is the
   same linear walk over a shorter prefix, and the over-read is the one partial page the loop
   truncates.
2. **The instrument is the same one and it agrees with itself.** The 100 % arm and
   `BenchmarkStreamReplay` read the identical stream by two different calls and land within the
   spread of each other's runs, which is what says `StateAt` is not a second implementation.
3. **This is not Gate 1 and must not be read as it.** `D-132:49` asks for *a named deployment's own
   p99, in its own environment, with its own payloads*. This is a loopback socket, a synthetic
   stream and a no-op fold on one workstation. What it does give the phase that opens the gate is a
   floor: at ~1.1 µs an event, a bound is worth taking below roughly 45 000 events and the crossing
   is where D-132 already put it.

**And the full-replay half, beside it, so Gate 1 has both numbers in one place** (added by S6 from
[PLAN] § *The snapshot gate*; `BenchmarkStreamReplay`, PostgreSQL 17.9, 2026-09-12, a 120-byte
payload, a no-op codec and a no-op fold, three runs each):

| Stream | `-benchtime` | ns/op, run by run | Full replay | ns/event |
|---|---|---|---|---|
| **10 000 events** | `20x`, `-count 3` | 14 881 024 · 13 050 254 · 12 914 297 | **12.91 – 14.88 ms** | 1 291 – 1 488 |
| **100 000 events** | `10x`, `-count 3` | 110 687 007 · 111 301 799 · 108 429 160 | **108.4 – 111.3 ms** | 1 084 – 1 113 |

The crossing against [[D-132]]'s ~50 ms trigger is **~45 000 events**, inside D-132's recorded
"somewhere above 30 000 – 50 000", so the trigger does not move. [[D-145]] carries the same table as
*the measurement that was taken and the reason it is not the one Gate 1 wants*; it is repeated here
because this file is where the phase that opens the gate looks for numbers, and because a cost is
not an argument ([[D-132]]:49) — which is the whole reason it is recorded here rather than argued
there.

### 49. A ledger whose table is empty publishes `now()` as its horizon, and every past `Issued` then reads `Expired`  `[medium]`

*Source: S5, found while writing the live horizon fixture.*

`Horizon` is *"an instant at or before the oldest row this ledger still answers for"* and the two
spellings the contract names are `MIN(recorded_at)` and `now() - retention`. `MIN` over an **empty**
table is `NULL`, and the obvious `coalesce(min(recorded_at), now())` then publishes **now** — after
which `standingOfAnAbsentRow` answers `Expired` for every key a caller minted before this instant,
including one minted a second ago for an operation that is in flight. It is conformant (the ledger
really holds nothing older than now) and it is the wrong answer for the first minutes of a
deployment, for any window in which the sweep emptied the table, and for the whole life of a
deployment whose first command is a retry.

`now() - retention` does not have it, which is exactly the case the contract's *"for a table that may
be empty"* clause already names — but the clause reads as a convenience and the failure it prevents
is nowhere stated. S5's own fixture works around it by writing a row before the horizon matters,
which is the shape a test may take and a deployment may not.

Owed: one sentence on the module page and in `_examples/event-receipts` saying that a ledger whose
table can be empty publishes `now() - retention` rather than `coalesce(MIN(...), now())`, with this
failure named; or a third spelling — `LEAST(min(recorded_at), now() - retention)` — recommended
outright.

### 50. The store's own concurrency check masks a defective claim, and which line of defence caught what is nowhere written  `[low]`

*Source: S5, found while writing `TestTwoCallersRaceOneKey`.*

A loser wrongly told `Recorded` appends at whatever version its token names. If it decided **before**
the claim, at the same version the winner decided at, the append is refused by `eventpg`'s own CAS
(`ErrConflict`) and no second copy lands — so a ledger with the `SELECT` before the `INSERT` looks
safe in any test whose racers both load first. The duplicate only appears when the loser loads
**after** its claim returned, which is what `Once` does, and which is why S5's race is written that
way and says so in a comment.

The consequence is a real one for a deployment: the receipt's value over the store's version check
is exactly the case of *two retries at different expected versions* ([SPEC] §1.2), and a deployment
that reads "the claim prevents both appends" without that clause will conclude its own ordering is
safe because its tests happened to be at one version. §1.2 has the sentence; what nothing has is the
statement that the two mechanisms are **layered** — the version check catches the same-version race
and the receipt catches the cross-version one — and that a test which exercises only the first
proves nothing about the second.

Owed: one paragraph on the module page beside the claim's order, and a sentence in D-142.

### 51. A store's page-level refusal denies a prefix the caller could read, and `StreamPage` decides which  `[medium]`

*Source: the phase-5 S5 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S5_GAPS.md`](EVENTSOURCE_P5_S5_GAPS.md) GAP-1). Driven live against PostgreSQL
17.9.*

`StateAt` over-reads at most one page and truncates in Go (`event/repo.go:325-356`), and the page is
scanned by the **store** before the kernel sees it. A row the store itself refuses —
`errRowOutsideSchema` → `event.ErrBackend` — therefore refuses every bounded read whose bound falls
in the same page, even when the requested prefix is entirely readable.

Driven at `StreamPage: 3` with the broken row planted at version **2** and the bound at version
**1**:

```
a wire type this declaration does not know   bound 1 → state="v1" err=<nil>
a recorded payload over the schema's bound   bound 1 → state=""   err=event: the store failed
```

The kernel's own refusals (`ErrUnknownType`, `ErrRevision`, `ErrUpcast`) refuse the **row** and a
bound below it reads normally; the store's refuses the **page it scanned**. So which historical reads
survive a corrupt row is a function of a page size no caller can see, and a corrupt version 2 denies
the operator the `StateAt(…, 1)` they would use to diagnose it.

`[medium]` because the answer is an honest refusal and never a partial state, so §INV-118 holds. The
behaviour is written down exactly once, in `event/eventpg/stateat_integration_test.go:296-299` (plan
correction 3), and nowhere a caller reads. The proper fix is the version ceiling on
`Store.ReadStream` that [SPEC] §"The store contract is not widened" deliberately refused and §INV-120
forbids — so what is owed inside this phase is the sentence and the missing term in the argument:
the over-read does not only buy *"one partial page of I/O"*, it also buys a refusal on a corrupt
tail.

Owed: one clause in `StateAt`'s doc comment, the same on both `event.md` pages beside `ErrBackend`,
and the missing term in [SPEC] §"The store contract is not widened, and the arithmetic is the
argument" or in the decision that records it as accepted.

### 52. `Expired` is reachable for an operation still in flight, with no clock skew at all  `[medium]`

*Source: the phase-5 S5 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S5_GAPS.md`](EVENTSOURCE_P5_S5_GAPS.md) GAP-2). Driven live against PostgreSQL
17.9. Neighbour of item 49, and item 49's recommended fix does not close it.*

`standingOfAnAbsentRow` (`event/receipt/resolve.go:120-125`) compares the caller's `Issued` against
the ledger's horizon and nothing else. A key minted before the retention window opened therefore
resolves to `Expired` while its writing transaction is still open — and [SPEC] §"Retention" tells the
caller that the difference between `Unresolved` and `Expired` is *"re-resolve, or **stop**"*.

Driven against a ledger publishing `now() - retention` with a one-minute retention and a
ten-minute-old `Issued`, with **no** clock skew applied:

```
PROBE in-flight-under-retention standing=[standing expired] horizon=…16:44:43 issued=…16:34:43
PROBE after-the-commit          standing=[standing found]
```

[SPEC] acknowledges the shape only as a skew consequence — *"A clock behind the database's answers
`Expired` for an operation that is in flight"* — and item 49 attributes it to the empty-table
`coalesce(min(recorded_at), now())` spelling. But this was driven on `now() - retention`, which is
item 49's own recommended fix, so the failure is a property of the comparison rather than of either
spelling.

`[medium]`: `Expired` is still a non-conclusion. It is neither a false `Found` nor a false "it did
not happen", the caller does not re-issue, and no duplicate append is reachable through it. What is
wrong is the analysis — the residue is bounded by the deployment's retention, which is minutes, not
by host-to-database skew, which is milliseconds, so *"why the error term is tolerable"* does not
cover this case. No behavioural fix is available: there is no row to lock, which is ES-07's hard
clause.

Owed: one clause on `Standing.Expired`; the retention attribution added to [SPEC] §"Retention"
beside the skew one, with what a caller holding very old keys does instead; the module page's advice
for `Expired` not reading as terminal without that caveat; and item 49 amended so its
`now() - retention` recommendation is not read as closing this.

### 53. Three shipped bookkeeping claims name things that do not exist  `[low]`

*Source: the phase-5 S5 code review (Round 1), 2026-09-12
([`EVENTSOURCE_P5_S5_GAPS.md`](EVENTSOURCE_P5_S5_GAPS.md) GAP-3).*

1. `event/eventpg/receipt_integration_test.go:32-35` asserts, in the present tense, that
   *"`_examples/event-receipts` publishes the same two, and
   `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares them byte for byte and in order — so
   'the reference implementation' is a fact this suite proved rather than a label a page applies."*
   Neither exists; `grep -rn` finds the test name only inside that comment. S6 owes both, and until
   it lands the sentence is false about itself — which is the failure this repository's own doc rule
   prices.
2. [PLAN] § *The complete set of paths phase 5 may add to the manifest* omits
   `event/refusal_test.go` and `event/refusalmessages_test.go`, which did move (S1's own
   `event-kernel-moved` regex correctly includes them), and lists `event/replay_test.go`, which
   exists and did **not** move. S6 re-checks the phase's whole manifest against that list and trips
   in both directions.
3. [PLAN] § *S5 — executed* says the `make api` diff is 117 lines. `git diff --numstat
   docs/api/surface.md` answers `116 0`; the extra line is the `+++` header. The substantive claim —
   that nothing of S5's is in it — is true.

Beside these, a **sixth** `Held.Complete` state was driven that §UC-248's five refusals do not name:
a completion issued after the claim's own transaction has committed answers
`sql: transaction has already been committed or rolled back` — a bare driver error rather than any
`receipt` sentinel — and leaves the committed append beside a `0..0` incomplete row. Fail-closed and
loud, and the residue is §UC-222's documented defect state, but the state itself is unnamed.

Owed: the example and the comparison test, or a narrowed comment; the two corrections to the plan's
allowed-path set; the 116; and one clause naming the after-the-commit completion, either on
`Held.Complete` or in [SPEC] §UC-248 as the ledger's error to raise.

**Sub-item 1 is closed by S6**, which was the phase that owed it: `_examples/event-receipts/main.go`
exists and publishes the same two claim statements, and
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved` (`scripts/docs_test.go`) compares them with this
suite's fixture byte for byte and in order, with a swapped-fixture control. The comment at
`event/eventpg/receipt_integration_test.go:32-35` is now true about itself. Sub-items 2 and 3 and
the unnamed sixth `Held.Complete` state stay open and are still `[low]`.

### 54. A `receipttest` conformance harness for a `Ledger` is owed and is not in this phase  `[medium]` — **CLOSED by the conformance round, 2026-09-12**

*Source: S6, per [PLAN] § *The conformance extension*.*

`receipt.Ledger` is the one genuinely new contract a third party implements, and its four
obligations are exactly the kind `eventtest` exists for: the claim is an `INSERT … ON CONFLICT
(key) DO NOTHING` followed by a `SELECT` of the same key **in that order, in one transaction**; the
horizon is monotone and comes from the database's clock; `Find` sees committed rows only; and
`Claim` and `Complete` run inside the caller's transaction while `Find` and `Horizon` run outside
one.

What phase 5 shipped instead is the falsifying half without the package: four decorators over the
reference implementation, each asserted to break the case that names it
(`TestFourLedgerDefectsEachBreakTheCaseThatNamesThem`), plus
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved`, which pins the published example to the statements
the live suite ran. So a third implementation is now *provable* and no harness exists.

**Why it is not here.** Publishing a conformance module for `Ledger` alone while `Park`,
`Generations` and `Effects` — three shipped application interfaces of the same shape — have none
would be a suite chosen by recency rather than by risk, and doing all four is a phase of its own.
That is the same call, with the same reasoning, that left the park's byte bound uncertified in
phase 4 (`## P4` item 70).

Owed: `receipttest.Run(t, factory)` with one section per obligation, its own defect inventory, and
the two anti-vacuity rules `eventtest` already applies — or, if the answer is the wider one, a
phase that publishes harnesses for all four application interfaces at once.

**Closed with the wider answer, minus `Effects`.** `eventtest.RunLedger` publishes seven sections —
`claim`, `repeat`, `claim order`, `unit of work`, `completion`, `horizon`, `transaction` — with a
ten-row defect inventory and both anti-vacuity rules, beside `RunGenerations` (five sections, six
defects) and `RunPark` (six sections, eight defects). They live in `eventtest` rather than in a
`receipttest` of their own because the runner, the three words and the three anti-vacuity rules are
already there and a second copy of those would be a second account of one rule; `scripts/event_test.go`
records what that costs the import graph. `Effects` is still uncertified and is a one-method sink
whose obligation ("what you do here must roll back with the unit") is not observable from outside
it — see the closeout's GAP-2 note.

### 55. A store's first position has no published origin, and the zero-`Mark` discriminator rests on it  `[low]`

*Source: S6, per [PLAN] **P-6**.*

`Wait` refuses the zero `Mark` on the ground that *"a position is drawn from an identity sequence
starting at one, so zero is never a number a store produced"*. Both shipped stores do start at one.
**No conformance section asks for it**: `eventtest.inventory()`'s twenty sections check ascent,
density, conservation and paging, and none of them asserts where a log's first position is.

The failure mode is bounded and that is why this is `[low]` rather than higher: a store that
assigned position zero would have its first event's mark **refused** at `Wait`'s door with
`ErrSpec`, which is a loud refusal on a legal store rather than a forged mark on an illegal one. A
caller would be told to mint again and could not.

Owed: either a `positions begin at one` clause in the `global order` section of `eventtest` — the
cheap half, since the section already reads the first page — or, if the origin is deliberately a
store's own choice, a second discriminator on `Mark` that does not rest on it.

### 56. `TestNoDocPromisesExactlyOnceDelivery` is English-only, and this phase added two more Russian pages to its blind spot  `[medium]`

*Source: S6, restating P1 backlog item 8 because the blind spot grew rather than because it is new.*

The walk carries two `wording` rows, English and Russian, and both are exercised — but only against
what each page happens to be written in, and the Russian row's `about` pattern is narrower than the
English one's. What this phase added to the tree is `docs/modules/ru/receipt.md` and a Russian wait
section on `docs/modules/ru/projection.md`: two more pages about **operation idempotency** and
**delivery**, which is the subject most likely in this repository's history to attract the phrase
*«ровно один раз»*.

**S6 ran the test itself** as part of the whole-package `./scripts/` arm and it is green, so the
English half is no longer merely assumed. What stays open is the Russian half's coverage: the row
matches, it is counted, and nobody has shown it would catch a promise written in the wordings a
Russian page would actually use for a *receipt* rather than for a projector.

Owed: extend the Russian `about` pattern to the idempotency vocabulary (`идемпотент`, `квитанц`,
`дедуплик`) and add a Russian fixture arm to
`TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` that uses it.

### 57. The published `jobs` mechanism has no module page, so D-142's reciprocal sentence has nowhere to live  `[low]`

*Source: S6, found while writing `docs/modules/{en,ru}/receipt.md`.*

[PLAN] § *The four decisions* asks for *"one sentence on `docs/modules/{en,ru}/receipt.md` and one
on `jobs`'s page"* saying which of the two key→verdict mechanisms a consumer reaches for. **There is
no `jobs` page.** `docs/modules/{en,ru}/` holds fifty-odd pages and none of them is `jobs`, and
`docs/modules/{en,ru}/Index.md` carries no `jobs` row — the subsystem is documented through
[[D-118]], [[D-119]] and [[FL-035]] and nowhere else a consumer would land.

S6 put the sentence on both `receipt.md` pages and pointed the reciprocal direction at [[FL-035]],
which is the document a reader of `jobs` does land on. That is the honest half of the obligation and
not the whole of it: a consumer who reaches for `jobs.EnqueueOnce` from the module index finds no
page at all, let alone one naming `receipt`.

Owed: `docs/modules/{en,ru}/jobs.md`, with the reciprocal sentence in it — which is a page about a
whole subsystem and therefore its own piece of work rather than a line S6 could add.

### 58. The receipts example sweeps with `runtime.Every` where the plan said a `jobs` periodic  `[low]`

*Source: S6, a deliberate deviation recorded rather than absorbed. The plan carries the correction.*

[PLAN] § *The roadmap close-out* specifies `_examples/event-receipts` with *"a `jobs` periodic that
sweeps"*. A `jobs` schedule needs `jobs.NewScheduler`, a `Queue`, a driver — `jobs/jobspg`, a second
module — its own schema version and a worker fleet to lease and run the placed invocation, all to
issue one `DELETE`. The example ships `runtime.Every("receipt-sweep", time.Hour, ledger.sweep)`
instead: the framework's own periodic `runtime.Runner`, supervised by the host exactly as the
projection beside it is, with no second schema and no second module in `_examples/go.mod`.

Nothing about retention's contract changes — the horizon is still computed in SQL from the same
configured retention, and the framework still prunes nothing. What is lost is the demonstration that
a sweep **can** be a durable scheduled job for a deployment that wants one operator-visible place
for periodic work.

Owed: one sentence on `docs/modules/{en,ru}/receipt.md` naming `jobs.NewScheduler` as the heavier
alternative, or a second example if a consumer asks for the scheduled shape.

### 59. Both `receipt.md` pages say the shipped example sweeps with a `jobs` periodic  `[medium]`

*Source: S6 review round 1, GAP-2.*

`docs/modules/en/receipt.md:299` — *"`_examples/event-receipts` runs one as a `jobs` periodic"* —
and `docs/modules/ru/receipt.md:306`, *"запускает его периодической задачей `jobs`"*. The example
ships `runtime.Every("receipt-sweep", time.Hour, held.sweep)`
(`_examples/event-receipts/main.go:361`) and imports no `jobs` package; `_examples/go.mod` requires
none. S6 recorded the deviation (its own deviation 2, item 58 above) and corrected the plan, the
close-out table and the `_examples/README.md` row — but not the two pages that describe the example
to a consumer. Item 58's `Owed` line asks for an added sentence about `jobs.NewScheduler`; it does
not record that the sentence already there is false.

Nothing misleads a consumer about the framework itself: the sweep is the application's either way
and the horizon contract is untouched. What it costs is a reader who opens the example expecting a
scheduled durable job, and a repository whose module page and whose backlog disagree about one file
in one change.

Owed: both pages naming the shape the example uses, with `jobs.NewScheduler` — if it appears at all
— named as the heavier alternative.

### 60. D-142 records that its reciprocal sentence lives on a page that does not exist  `[medium]`

*Source: S6 review round 1, GAP-3.*

`docs/ai/decisions/D-142-…:250-252` reads *"That sentence is on `docs/modules/{en,ru}/receipt.md`
and on the `jobs` page"*. There is no `jobs` page, which is exactly what item 57 above records. The
plan carries the correction and both `receipt.md` pages implement it by pointing at [[FL-035]]
(`en:332`, `ru:339`); only the decision was left asserting the page.

The decision's argument is sound and its by-symbol adjudication of `jobs` is accurate — all fifteen
symbols it names exist, `jobs/placement.go:18`'s `PlacementOnce` included. What is false is one
claim about where a sentence lives, in the file a later agent reads to learn whether the obligation
was met, which is the binding layer rather than a description.

Owed: D-142's sentence naming `receipt.md` and [[FL-035]], or saying the `jobs` page is owed and
linking item 57.

### 61. The new usage guide is in no index a reader reaches it from  `[medium]`

*Source: S6 review round 1, GAP-4.*

`docs/usage-guides/event-sourcing.md` has no row in `docs/Index.md`'s **Usage guides** list (which
names ent, gorm, migrations, model-generation and tenancy), none in either
`docs/modules/*/Index.md` guide list, and no link from any module page. The only pointer in the
tree is `docs/roadmaps/Roadmap.md:443` — the file whose own rule is that it holds only what is not
built, so the single link is on the page the close-out will eventually delete it from.

`CLAUDE.md` states the rule without qualification: *"when you add a doc, add its row to the
directory's `Index.md` in the same change. An index that does not list a file is worse than a
missing file — an agent trusts the index and stops looking."* Mitigating: that list is already
incomplete at HEAD — `usage-guides/repository.md` is missing from it too — so this is one more
absence in a list nothing enforces rather than the first one.

Owed: the row in `docs/Index.md`, plus a `See also` link from `event`, `projection` and `receipt`.

### 62. The UC-032 byte-identity check the plan names as the use-case obligation's gate did not ship  `[medium]`

*Source: S6 review round 1, GAP-5.*

[PLAN] § *The roadmap close-out*, the **Use cases** row, names as its `Held by` *"a doc check
asserting UC-032's two sections are byte-identical to their predecessor"*. No such check exists:
`grep -rn 'UC-032' scripts/*.go` is empty, S6's four new checks are
`TestNoProjectionGuideRestatesHighestAsThePagesLastPosition`,
`TestNoEventGuideOffersATimestampBoundary`,
`TestTheThreeObligationsAWaitCannotCheckAreStatedTogether` and
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved`, and the checkpoint's counted `-run` arm lists
eight names none of which is it. The section records three deviations from its own text and this is
not among them.

*(Counts as of the finding. Closing GAP-1 added `TestEveryGoFenceInTheEventSourcingGuideIsCompiled`
and `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave`, so S6's new checks are six and the counted
arm lists ten. Neither is a UC-032 check and this item is untouched by that.)*

The property holds today and was verified directly: `git diff` on UC-032 is `+20 −0`, all twenty
lines a `## See also` section appended after `Out of scope`, with no clause of `What must hold`
changed. What is missing is the falsifier — *"UC-032 is not silently widened"* is the constraint
this phase's framing states twice and it is now held by nobody, so a future section that adds one
clause gets no report from any arm of `make unit`.

Owed: a check in `./scripts` comparing UC-032's two sections against a recorded copy, with a
control fixture that changes one clause and is reported — or the plan's `Held by` cell corrected to
name what actually holds it.

### 63. Both new examples accumulate state across runs, so their pasted output is not what a second run prints  `[low]`

*Source: S6 review round 1, GAP-6.*

`_examples/event-wait` and `_examples/event-receipts` create their tables `IF NOT EXISTS` and
delete nothing, so the wait example's parked letters and the receipts example's streams survive the
process. Re-run at HEAD against the same database, the record's
*"quarantined=0, 1 letter(s) held"* reads *"quarantined=1, 2 letter(s) held"*, and
*"range=1..1"* reads *"range=2..2"*. Every line keeps its shape and every assertion inside both
programs still holds — the numbers are functions of how many times the example has been run.

Nothing is wrong and `_examples/README.md` promises no numbers. What it costs is a reviewer: the
plan pastes this output as evidence, and a reader re-running it to check that evidence cannot tell
accumulation from drift without reading the schema.

Owed: a fresh stream id and park identity per run, or one sentence beside the pasted output saying
the numbers grow with the run count.

## Closeout

*Source: the closing round of 2026-09-12, answering
`.agents/artifacts/audit/EVENTSOURCE_CLOSEOUT.md`. It closed Definition-of-done items 8 and 10,
item 1's forbidden-name half and item 6's tenancy half. Everything recorded here is `[medium]` or
`[low]` and is left alone by the policy at the top of this file, except where a row says it was
already closed in passing.*

### 64. GAP-3, the wait's understated cost — **CLOSED in this round**  `[medium]`

`docs/modules/en/projection.md`, `docs/modules/ru/projection.md` and
`docs/usage-guides/event-sourcing.md` all said **80** `SELECT`s a second for a four-member cover,
and *"fifty concurrent waiters … about a thousand a second"*. The arithmetic had dropped the
`Park.Sequences` term and used twenty polls a second against four reads instead of five.

All three now carry the derivation as a table and the corrected numbers: `1 + |cover|` a poll
healthy and `1 + 2 x |cover|` during a rebuild, so **100** a second at the 50 ms default, **120**
while that generation's park queue is not empty, **180** rebuilding, **200** rebuilding with a
non-empty queue, and **five thousand** for fifty concurrent waiters. The numbers are the ones
`TestAWaitStartsNothingAndSavesNothing` and `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles`
already measure — four loads a poll recorded, eight fresh, one `Sequences`, zero `Holds` while
healthy — so the pages and the tests now agree. The runbook carries the same table.

### 65. GAP-4, `Roadmap.md`'s summary row for item 15 — **CLOSED in this round**  `[medium]`

The row gave the blocker as *"a named aggregate; the vocabulary, the in-memory store and the
conformance suite are delivered"*, four phases behind its own §15. It now names what §15 says is
left: a retention/archival path and an `eventpgfx`, each blocked on a consumer that needs it.

### 66. GAP-5, ES-05's missing *degraded* verdict is argued only here  `[medium]`

Unchanged and deliberately so. ES-05 part 3 asks for *timeout **or** degraded*; only timeout
shipped, and the reason — a phase is in-process and a waiter usually is not that process — is still
written only in `## P5` item 1 of this file. The standard ES-06 sets is one layer above: in capitals
on the module page and in an ADR.

Owed: one paragraph on `projection.md` in both languages saying why a wait cannot report a phase,
or a second verdict that does.

### 67. The consumer graphs are local-replace only; there is no tag-resolving arm for `event`  `[medium]`

`check-event-consumer` resolves the three graphs through a `replace` onto this tree with
`GOPROXY=off`, which is what lets it live inside an offline `make check` before the first tag. What
it therefore does **not** prove is the half `scripts/otel-consumer.sh` proves at release time: that
`go get github.com/frostgrove/vv/event/eventpg@v0.1.0` resolves from a proxy, with no replace, to a
directory outside this repository. `scripts/release.sh` runs `otel-consumer.sh` and
`i18n-consumer.sh` and has no event equivalent.

Owed: an event arm in `release.sh` on the `otel-consumer.sh` model, or on `i18n-consumer.sh`'s
`git archive` + `file://` proxy model, which needs no published tag.

### 68. `docs/usage-guides/` is English-only except for the runbook  `[low]`

The bilingual convention in this tree is `docs/modules/en` and `docs/modules/ru`.
`docs/usage-guides/` has seven English pages and no `en/` directory, so the Russian runbook went to
`docs/usage-guides/ru/event-operations.md` — English at the top level where every other guide is,
Russian in a subdirectory the way `docs/modules/ru` does it. It reads as if the other six guides
have a missing Russian counterpart, and they do not: they never had one.

Owed: either move the seven English guides under `docs/usage-guides/en/` and make the split
symmetric, or one sentence in `docs/Index.md` saying the guides are English-only and the runbook is
the exception. Nothing is wrong today; a reader guessing at the shape will guess wrong.

### 69. GAP-1 and GAP-2 of the closing audit are untouched, and both are `[high]`  `[high]`

Recorded here so that they are not read as closed by a round that did not touch them. **GAP-1:**
nine `[critical]`/`[high]` entries sit open in `## P1 — deferred` and `## P2`, of which the audit
verified four have had their own stated closure conditions met and were never flipped — items 1, 4,
6 and 8. **GAP-2:** `Ledger`, `Park` and `Generations` are three interfaces a consumer must
implement and nothing a consumer can run checks any of them; the defect is measured in this
repository by `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem`.

Both are the audit's remediation steps 1 and 2 and neither was in this round's brief, which named
Definition-of-done items 8, 10, 1 and 6. They are blocking severity and they are still open.
