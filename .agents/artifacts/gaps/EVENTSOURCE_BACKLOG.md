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

### 2. `eventmemory` conformance holes  `[critical]`

No transaction is ever driven through a second store value over the same log, so keying the
transaction by the `*Store` instead of the `*Log` survives — which is exactly the escape INV-041
exists to close. Every transaction in the suite claims exactly one stream, so the claim set is
untested in both directions: a two-stream commit can leave a stream permanently unwritable.

### 3. The value walk's absence arms are unpinned  `[critical]`

`event/comparison.go` — the walk's *absence* arms survive deletion: `sameEntries` comparing a map
key with itself, `same`'s invalid-value arm, and a codec that fills a decode buffer to the payload's
width and exposes it only through an unexported field passes `RoundTrip` with nil.

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

### 59. `eventtest` never walks the log while a lower position is uncommitted, so a cursor that skips an in-flight position is invisible to it  `[high]`

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
