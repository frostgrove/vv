# faults · sqlfault · probe · catalog — one refused write, told back as every mistake the payload made

**Covers:** `github.com/frostgrove/vv/crud/decorators/faults`, `github.com/frostgrove/vv/crud/sqlfault`, `github.com/frostgrove/vv/crud/probe`, `github.com/frostgrove/vv/crud/catalog`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — the arithmetic underneath is the most carefully argued code in the repository, and the wiring on top of it ships one option whose three documented verbs do nothing, a headline claim that is false in both languages, a family of constraints dropped without saying so, and a probe that fires on failures no constraint caused and can move a 422 to a 409. The edge pass adds several ways the declared safety controls betray their caller: an explicit source can make the probe consult a different database, CodeOnly does not survive the final field-mapping hop, one Skip can disable two distinct constraints, and ordinary conditional wiring can crash on the first refusal; probe failure is bounded but lacks a decorator-level cancellation proof.

## What a consumer is actually trying to do

Someone posts a signup form. The email is taken, the organisation they picked
was deleted an hour ago, and the invite code belongs to somebody else. The
database refuses the write and says one word about one of those three, because
that is what a database does — the first constraint it reaches ends the
statement. The user fixes one field, posts again, and learns the second thing.
Three round trips to learn what was all knowable at the first.

So the first thing someone wants here is the whole set, at once, attributed to
a field rather than to a column or a constraint name. This subsystem gets them
as far as the *model field* — `Email`, not `email`, and `OrgID`, not `org_id`.
Turning that into the key the client actually sent is a further hop owned by the
transport layer, it needs the request body to still be around, and it declines
for a form body, an XML body, or a name that folds to two places in the
document. Anyone who reads "names the field" as "names the JSON key" is going to
be surprised at least some of the time.

The second thing is narrower and more urgent: they want the response to name
*something*. A 409 with an empty body is a support ticket. A 409 that says
`{"field": ["email"], "error_code": "unique"}` is a red outline on a form. Most
teams arrive wanting only this, and discover the rest later.

Most of what a real form produces is not a collision at all. It is a blank
required field, a name longer than the column, a date the parser refused. A
consumer who arrives at a subsystem headlined "every mistake the payload made"
expects those to be part of the set, and they are not — and what they *do* get
for them differs on every engine.

The third thing is what they want six weeks later, when the product has tenants.
The moment a failed write can say "that email is taken", it can also say "that
email is taken" about a row belonging to somebody else's company. The person
wiring this needs to know that they have built an existence oracle, and needs a
way to narrow it without turning the feature off.

And underneath all three: none of it may cost anything on the happy path, none
of it may turn a truthful refusal into an opaque 500, none of it may change the
status a client already branches on, and all of it must break at start-up rather
than at 3am — if the schema and the model disagree, the process should refuse to
boot on the deploy that broke them, and it should say which deploy that was.

## Happy cases

### H-FAULTS-01 — The 409 names the field
**Who:** a backend author with a signup endpoint and a `users_email_key`
**Wants:** the refusal to say `Email`, so the form can put a red outline on it
**Story:** They read the error section's first two steps, add
`sqlfault.New("postgres")` to the handle and `faults.Enrich[User, int64]()` to
the `Bind` list, redeploy, and post a duplicate email.
**Must hold:**
1. The body carries a stable machine code for the duplicate.
2. The body carries the model field the collision happened at.
3. The documented minimum is enough to get both.
**Today:** 🟡 partial
**Evidence:** (1) holds and is free: `crudsql.Postgres` already builds its own
classifier (`crud/adapter/crudsql/crudsql.go:159,164-166`), so the documented
first step adds a line that was already there. (2) does **not** hold at that
wiring, for a unique violation, on any engine. On PostgreSQL a unique violation
names the constraint and the table and *no column* — pinned at
`errs/sqlerr/classify_test.go:448-453` — and on the other three the driver names
nothing structural at all (`errs/sqlerr/mysql.go:45-51`, `mariadb.go:36-42`,
`sqlite.go:46-60` all return `errs.Source{}`). `enricher.resolve` returns early
when `len(v.Source.Columns) == 0` (`crud/decorators/faults/faults.go:142-144`),
so the path stays nil. `sqlfault`'s own comment states the rule the docs do not:
"Even PostgreSQL names one only for 23502 — a unique violation reports the
constraint and the table and no column" (`crud/sqlfault/catalog.go:57-58`).
(3) is therefore false for the violation the module is sold on, and the docs say
otherwise in both languages: `docs/modules/en/faults.md:25` and
`docs/modules/ru/faults.md:25` both say the one-liner "names the model field";
`README.md:1083-1090` is the same claim.

Two routes reach the field, and they are not equivalent. The **probe route** —
`catalog.Load`, then `faults.WithProbe(probe.Full(cat))`, which is what
`README.md:1093-1104` and both usage guides lead with — costs three lines and
three imports, builds no second handle, names no second engine string, and works
on all four engines: the probe's own violation carries a path, and `fold` moves
it onto the driver's violation, matching by constraint where there is one and by
code where there is not (`crud/probe/full.go:199-238`). The **classifier
route** — `sqlfault.WithColumns(sqlfault.FromCatalog(cat))` — is PostgreSQL-only,
because `fill` needs both a table and a constraint name and returns untouched
without them (`crud/sqlfault/catalog.go:66-69`), and three of the four drivers
supply neither.
**If not ready:** Today the honest instruction is "skip step 2, go to step 3" —
which is not what either doc says, in either language. The classifier route
additionally costs a `catalog.Load`, a second construction of the same `*sql.DB`
and the engine string a second time, and the snippet does not compile in either
guide: `docs/usage-guides/ent.md:1336` uses `src` and `:1340` redeclares it with
`src :=` and nothing new on the left, and `docs/usage-guides/gorm.md:1255,1259`
is the same paste with `db` — a `*gorm.DB` in that guide, which uses `sqlDB` at
`:436`. Closing it is a documentation fix in four files plus a constructor that
does the assembly (see *The DX this should have*).

### H-FAULTS-02 — The blank required field, the too-long name, the unparseable date
**Who:** the same author, whose form's commonest failure is not a collision at all
**Wants:** the same treatment for the violations that actually happen most
**Story:** A user posts with the organisation name left blank and a display name
of 300 characters. The database refuses on `NOT NULL` or on the length.
**Must hold:**
1. The code says which of the three it was.
2. The field is named, or the response says it could not be.
3. The answer does not depend on which engine is underneath.
**Today:** 🟡 partial, and it is the case where the cheap wiring does best
**Evidence:** (1) holds everywhere. `23502`, `22001`, `22003` and `22P02` on
PostgreSQL (`errs/sqlerr/postgres.go:20-24`), `1048`/`1364`, `1406`, `1264` and
`1366` on MySQL and MariaDB (`errs/sqlerr/mysql.go:26-32`,
`mariadb.go:23-29`), subcode `5` on SQLite (`errs/sqlerr/sqlite.go:24`). All
resolve to `errs.CodeRequired`, `CodeTooLong`, `CodeOutOfRange`,
`CodeInvalidFormat`, all `KindValidation` (`errs/codes.go:76-81`), so the status
is 422 and not 409 ([[D-049]]).

(2) is the asymmetry nobody has written down, and it is the exact inverse of
H-FAULTS-01. `postgresSource` fills `Columns` from `ColumnName` whenever the
driver populated it (`errs/sqlerr/postgres.go:61-63`), and PostgreSQL populates
it for `23502`. So `faults.Enrich[T, ID]()` **alone** names the model field for a
missing required field on PostgreSQL, with no catalog and no probe — pinned at
`errs/sqlerr/classify_test.go:456-459`, three lines below the unique assertion
that says the opposite. On MySQL, MariaDB and SQLite it names nothing, because
those `*Source` functions return `errs.Source{}` unconditionally. And
`22001` — too long — carries no fields even on PostgreSQL
(`errs/sqlerr/classify_test.go:464-470` asserts the corpus records none).

(3) therefore fails, and no route closes it: the probe replays none of these
([[D-042]], `docs/ai/usecases/modules/faults/UC-017-...md:111-117`), and the
classifier route cannot help because there is no constraint name to look up by.
So the answer for the commonest failure a form produces is: a code always; a
field for `required` on PostgreSQL only; a field for nothing else anywhere; and
never a second violation.
**If not ready:** On the three engines that name no column they map the code to
a field in the handler, which means the handler already knows the schema — which
is the thing this subsystem exists to stop. Closing it properly is out of
[[D-042]]'s reach by design. Closing the *documentation* is one honest sentence:
the minimum names the field where the driver named a column, which on PostgreSQL
is `NOT NULL` and never a unique key, and the probe is what names it for a
collision.

### H-FAULTS-03 — The tenanted unique key names no field at all
**Who:** the same author, now on `UNIQUE (tenant_id, email)`
**Wants:** the same red outline on `email` that the single-column key gave them
**Story:** They add tenants. Every unique index in the schema grows a
`tenant_id` in front of it. The 409 stops naming a field and they cannot tell
whether the wiring broke.
**Must hold:**
1. A composite unique violation attributes to something the form can use.
2. If it cannot, the response says so rather than looking like a considered
   table-level answer.
**Today:** ❌ missing
**Evidence:** Neither holds, and (1) is a deliberate policy nobody has revisited.
`resolvePath` maps each column to a model field and then takes
`commonPrefix` of the results (`crud/decorators/faults/faults.go:169-183`). Two
flat fields share no prefix, so the path is empty. The library's own test pins
exactly this and pins that it is *not* approximate:
`TestACompositeUniqueYieldsOneViolationAtTheCommonAncestor`
(`crud/decorators/faults/faults_test.go:168-196`) asserts the path is empty, the
violation is not marked approximate, and the single-column control still
answers `"Title"`. The code's own comment invites the challenge: "The per-column
form is what a form-binding UI wants and it says two things that are each false
on their own, so it is a policy nothing asks for yet." A release-readiness sweep
is where someone asks. `UNIQUE (tenant_id, email)` is the single most common
unique key in a tenanted product, which makes this the *ordinary* case for a
large share of consumers, not an edge one. And (2) fails on top: `Approximate`
is deliberately false, so downstream cannot distinguish "no field, by design"
from "no field, we gave up" — and `Violation.MarshalJSON` drops `Approximate`
anyway (`errs/violation.go:73-76`), so the client sees `error_code` with no
`field` either way.
**If not ready:** With the probe wired the escape hatch is better than a
constraint name: the probe's own violation carries `Source.Columns` in key order
and `Source.Constraint` on all four engines
(`crud/probe/full.go:242-252`), and `fold` copies both onto the driver's
violation where they were empty (`crud/probe/full.go:223-238`). So a handler can
map `["tenant_id","email"]` to `email` itself. Without the probe there is only
`Source.Constraint`, which is PostgreSQL-only. Closing it needs a decision, not
code: either a per-column form behind an option, or a rule that drops key parts
the write did not touch before taking the prefix, so `(tenant_id, email)` with a
tenant supplied by the gate resolves to `Email`.

### H-FAULTS-04 — All three form errors in one response
**Who:** the same author, a week later, tired of the three-round-trip form
**Wants:** the taken email, the missing organisation and the taken invite code in
one body
**Story:** They load the catalog at start-up, pass `probe.Full(cat)` to
`faults.WithProbe`, and post the bad payload once.
**Must hold:**
1. One response carries all three, each with its own code and its own field.
2. Nothing is invented — a payload with one real problem yields exactly one.
3. A capped or failed answer says it is incomplete rather than looking complete.
4. The same failing request twice produces the same body.
**Today:** ✅ ready, with a ceiling that has to be stated
**Evidence:** All four hold, on all five engine targets. This is the part that is
finished. The positive control is `TestOneFailedWriteBecomesEveryViolationItCaused`
(`test/integration/probe_test.go:272`) and its negative twin is
`TestAPayloadWithOneRealViolationYieldsExactlyOne` (`:322`) with a third leg,
`TestTheSamePayloadWithARealMissingParentYieldsTwo` (`:374`), so a probe that
closed the false-positive hole by dropping foreign keys altogether fails rather
than passes. Caps set `Partial` (`crud/probe/full.go:185-187`,
`test/integration/probe_test.go:485`); determinism is
`TestTheSameFailingRequestTwiceProducesTheSameBody` (`:835`), whose engine count
assertion at `:870` is what keeps a skipped target from reading as a pass.

**The ceiling, which the docs state and this case must not skip:** "every
violation" means every violation the probe can replay from a value. A CHECK, a
NOT NULL, a length limit, a range or an enum membership is not in the set, and
that is [[D-042]]'s argument rather than an omission — UC-017's own Status says
so at `docs/ai/usecases/modules/faults/UC-017-get-every-error-for-one-payload-at-once.md:111-117`.
Swap the invite code for a CHECK and this story fails. Four kinds of unique key
are outside it too, and that is H-FAULTS-05.
**If not ready:** —

### H-FAULTS-05 — The constraint the probe will never probe, and never says so
**Who:** a Postgres shop with soft deletes: `UNIQUE (email) WHERE deleted_at IS NULL`
**Wants:** the one constraint they wired this for to be in the answer
**Story:** They add the probe for the signup form. It reports the foreign key
and the invite code and never the email, because the email key is partial. The
response says `partial: false`.
**Must hold:**
1. A constraint the probe cannot replay is either probed or declared unprobed.
2. An incomplete answer is marked incomplete.
3. The consumer learns which of their constraints are outside the set before
   production, not from a support ticket.
**Today:** ❌ missing
**Evidence:** All three fail, and the dropping is deliberate and tested.
`reproducible` returns false for a partial index, a deferrable constraint, a
prefix key and an expression key part (`crud/probe/plan.go:147-158`), and
`candidatesFor` then `continue`s past it silently (`:84-90`). `bind` drops more,
per row: a key part the insert did not write — a database default, a trigger —
and any unique key with a NULL part under the default NULLS DISTINCT
(`crud/probe/plan.go:248-300`). None of these sets `Partial`; `Partial` is set
only by a cap or a probe error (`crud/probe/full.go:67,185-187`). The live test
`TestTheUnreproducibleKeyIsNeverProbedAndItsPlainTwinIs`
(`test/integration/probe_test.go:448-482`) asserts the drop and its control, and
never asserts `Partial` — so the silence is the specification.

The shapes are not exotic. `UNIQUE (email) WHERE deleted_at IS NULL` is the
default soft-delete schema on PostgreSQL, `LOWER(email)` is the default
case-insensitive email, and `email(191)` is the default on MySQL utf8mb4 before
`innodb_large_prefix`. In all three the app's most important constraint produces
no term and the consumer is handed an answer that presents itself as complete.
The library's own argument against this is already written, one file over:
`probe.Skip` refuses an unknown constraint name because "a silent no-op would
turn the control off on the deploy that renamed the constraint"
(`crud/probe/declare.go:24-27`). Here nothing had to be misspelt.

**This contradicts UC-017's own Status, and one of the two has to move.**
Guarantee 6 — "complete, or explicitly marked incomplete" — is marked `yes` since
phase 7 (`docs/ai/usecases/modules/faults/UC-017-...md:90`), while the same
document's prose four paragraphs down says the four unprobeable key shapes are
"a narrowing: the answer is short, never wrong" (`:117`). Short and unmarked is
not what guarantee 6 says. Either the row is qualified or this blocker is
overstated; whoever fixes one must touch the other.
**If not ready:** They read the probe's source to work out which of their
constraints survive. Closing it is the inverse of `Skip`: a declaration-time
list of what will never be probed and why, for a start-up log, plus a
`probe.Require(names...)` that refuses at `Bind` when a named constraint is not
reproducible. A soft-delete partial index then becomes either a boot failure or
a written-down acknowledgement.

### H-FAULTS-06 — Saving a profile without changing the email
**Who:** every consumer of this library, on the verb that is most of their traffic
**Wants:** an edit that leaves a unique column alone not to report the row
colliding with itself
**Story:** A user opens their profile, changes their display name, and saves.
The email in the payload is the one already stored. The update is refused, or
worse, succeeds and the probe reports `unique` on `Email` anyway.
**Must hold:**
1. A row does not collide with itself.
2. A composite key whose unchanged half is not in the change set is still
   checked, against the stored value rather than a copy.
3. Neither depends on the endpoint sending every field.
**Today:** ✅ ready on (1) and (2), with (3) as a stated condition
**Evidence:** (1) is one line with the failure written next to it: `bind` sets
`t.own` from `row.ID` when `row.HasID` — "Without this a key the write did not
change reports the row colliding with itself" (`crud/probe/plan.go:295-297`) —
and the live twin is `TestAnUpdateDoesNotReportARowCollidingWithItself`
(`test/integration/probe_test.go:423`). (2) is the one place the probe reads a
value it was never given: [[D-010]] drops a column whose value already matches
the stored one, so the unchanged half of a composite key has no value in the
change set, and the term binds it as `refCur` — read from the row the update did
not change, in SQL, rather than from a copy that may be stale
(`crud/probe/plan.go:264-268`, `crud/decorators/faults/probe.go:269-275`).
Update is also the only mode in which `restrict` terms exist at all: an insert
cannot break an inbound foreign key (`crud/probe/plan.go:198-201`).

(3) is the condition, and it is not stated anywhere a consumer reads. `t.own` is
set only when `row.HasID`, and `HasID` comes from `e.meta.ID(m)` succeeding on
the model handed to `Save` (`crud/decorators/faults/probe.go:254-262`). A
keyless `Save` — the create path — correctly has no `own`; an `Update` goes
through `updateRequest` and carries the id it was called with. So the guarantee
holds for both shipped verbs. A consumer who builds their own `probe.Request`
for a hand-written path, which nothing forbids, loses it silently.
**If not ready:** — the case is green. It is written down here because nothing
else in the repository tells a consumer that editing a row is safe, and the
condition it rests on has no test name that says it.

### H-FAULTS-07 — The nightly sync that writes the same row twice
**Who:** a team running idempotent ingestion — a webhook handler, a "create or
update by external id" job
**Wants:** the key their upsert absorbs to produce no violation, and the ones it
does not absorb to still produce theirs
**Story:** The sync writes a row with a known id. The engine's conflict clause
swallows the primary-key collision. The row also carries an email another record
already owns, and that one is a real refusal.
**Must hold:**
1. A key the statement's own conflict clause absorbed produces no violation.
2. A key it did not absorb still does.
3. Which keys those are is the dialect's answer, not a guess.
**Today:** 🟡 partial
**Evidence:** (1) and (3) hold and somebody thought hard about them.
`probe.Request.Upsert` says the statement carried a conflict clause
(`crud/probe/probe.go:66-68`), a keyed `Save` sets it because that is the upsert
path under [[D-011]] (`crud/decorators/faults/probe.go:259-262`), `planFor`
drops the unique candidates the statement swallowed
(`crud/probe/plan.go:195-197`), and which ones those are comes from the dialect
through `crud.UpsertScope` (`crud/probe/plan.go:238-244`,
`crud/dialect.go:36-49`). `TestAnUpsertSkipsTheConflictsItsOwnTargetSwallows`
(`crud/probe/full_test.go:257`) pins it.

(2) is where it goes quiet. `swallowed` returns `true` — every unique key — for
"ON DUPLICATE KEY UPDATE, and anything unknown" (`crud/probe/plan.go:242-243`). Only
`crud.Postgres` and `crud.SQLite` implement `UpsertScope`
(`crud/dialect.go:188-189`); MySQL and MariaDB fall to the default, which is
right for their statement, and any fifth dialect a consumer supplies under
H-FAULTS-26's SPI falls to it too, which is not. On such a dialect every unique
term is dropped from every keyed `Save`, the probe issues no statement at all,
and the answer still says `partial: false`. That is H-FAULTS-05's silence
reached by a second road, on the verb a sync job uses for every row.
**If not ready:** Nothing to write by hand — the drop is invisible, so the
consumer does not know there is anything to work around. Closing it is the same
`Require`/declare-what-was-dropped mechanism H-FAULTS-05 needs, plus one line in
`crud.UpsertScope`'s doc saying a dialect that does not implement it turns the
probe's unique half off for upserts.

### H-FAULTS-08 — The probe must not confirm another tenant's row
**Who:** whoever ships the multi-tenant SaaS on `security.Gate`
**Wants:** "this email is taken" to mean taken *in this workspace*, and the same
for every other question the probe asks
**Story:** They already run `security.Gate(policy)`. They add the probe. Someone
notices that a signup attempt now reports a collision against a row in a
workspace the caller has never seen.
**Must hold:**
1. Every read the probe issues carries the narrowing the caller's own reads do.
2. Wiring that narrowing is one option, not a second policy.
3. Wiring the probe without it is visible before production.
**Today:** 🟡 partial
**Evidence:** (2) holds well: `probe.WithScope` takes the exact shape
`security.Policy.Scope` already has (`crud/probe/options.go:70`,
`crud/decorators/security/security.go:62`). (1) holds for *unique* terms and for
nothing else. `renderTerm` passes the scope to `renderUnique` and to neither of
the other two (`crud/probe/sql.go:80-89`); `renderForeignKey` reads the parent
table unnarrowed (`crud/probe/sql.go:117-142`) and `renderRestrict` reads the
child table unnarrowed (`crud/probe/sql.go:144-165`). So the probe still answers
"org 4711 does not exist" and "rows still point at this row" about data the
caller may never see. The option's own doc comment states it and offers `Skip` as
the control (`crud/probe/options.go:66-69`), which turns the check off rather
than narrowing it. Both halves are pinned by
`TestTheScopePredicateNarrowsAUniqueTermAndNotAForeignKeyOne`
(`crud/probe/full_test.go:526`), whose name is the finding.

The narrowing is also already recorded as settled: `docs/ai/usecases/Index.md:172-176`
files it as phase 7's outcome and says "`Skip` is the control there". This sweep
disagrees that it is settled — a control that removes the check is not a
narrowing of it — and the disagreement has to be resolved in one place, because
an owner reading a blocker beside an index entry that calls it done has no way
to tell which is current.

(3) does not hold at all. `crud.Chain` applies the innermost middleware first
(`crud/repo.go:110-117`), so `faults.Enrich` cannot see the gate above it, and
nothing at declaration says "there is a scope on this repository and the probe
is not using it". The short wiring is the leaking one, and the twentieth
resource is the twentieth chance to write `security.Gate(policy)` and forget
`probe.WithScope(policy.Scope)`.
**If not ready:** They remember, twenty times, and they narrow only one of the
three term kinds even when they do. The refusal in (3) cannot be built inside
`faults` — it wraps nothing and sees nothing above it. It belongs in
`security.Gate`, which wraps the enricher and reaches it through `Next()`
(`crud/decorators/security/security.go:176`, [[D-061]]) — and it must ask
through an optional interface the enricher implements, not by importing
`faults`, or one decorator starts knowing another's options.

### H-FAULTS-09 — Our tenant boundary is RLS, not a Go predicate
**Who:** a PostgreSQL shop that enforces isolation in the database with
`ROW LEVEL SECURITY` and `SET LOCAL`
**Wants:** the probe's statement to see the same rows every other statement in
the request sees
**Story:** Their middleware opens a transaction, issues
`SET LOCAL app.tenant = '42'`, and every policy in the schema keys on it. They
add the probe. It runs outside that transaction.
**Must hold:**
1. The probe's read is subject to the same session state the write was.
2. Where it cannot be, there is a control that narrows it anyway.
**Today:** ❌ missing
**Evidence:** Neither holds and neither is written down anywhere. The probe's
statement runs at request time, and with no executor in the context it runs on
whatever pooled connection it gets: `ex = req.Source`
(`crud/probe/full.go:124`, with the comment explaining the `ReadWrite` case and
not this one). Session state does not follow a pooled connection.
`SET search_path`, an RLS boundary established with `SET LOCAL`, a temp table:
none of it is there. For (2), `probe.WithScope` takes
`func(context.Context) (crud.Predicate, error)`, and an RLS deployment has no
`security.Policy` to hand it — the whole point of RLS is that the predicate
lives in the database. `Skip` remains, which removes the constraint from the
answer.

Note the asymmetry with the case above: a `security.Gate` shop leaks two of
three term kinds and has an option for the third; an RLS shop leaks all three
and has nothing to pass. This is the only case in the file where `WithScope` has
no argument available at all.
**If not ready:** They enable the probe only inside their own transaction, which
on PostgreSQL means also enabling `WithSavepoints`, which puts two statements on
every happy-path write (`crud/probe/options.go:46-53`) — or they do not use the
probe. Closing it means the probe running on the request's executor when there
is one, which it already does, plus a documented instruction that an RLS
deployment must keep the write inside the transaction that set the variable. The
instruction does not exist.

### H-FAULTS-10 — "You cannot delete this organisation, it still has projects"
**Who:** an internal admin tool
**Wants:** the delete refusal to say which children are in the way
**Story:** An admin clicks Delete on an org that still owns projects. The
foreign key refuses. They want the same treatment writes get: the right code, a
field where one exists, and every child relationship that is blocking, not only
the first.
**Must hold:**
1. The code says what happened — this row is still referred to.
2. `WithProbeFor("Delete", …)` does what its own doc says.
3. Where two different children block the delete, both are reported.
**Today:** ❌ missing, and (1) is engine-dependent in a way nothing states
**Evidence:** (1) already holds on **MySQL and MariaDB**, and the round-1 draft
of this file was wrong to say `errs.CodeRestrict` exists in one place. Both
engines separate the two foreign-key directions by native number — 1451 is
"cannot delete or update a parent row" and 1452 is the child direction — so a
delete blocked by children comes back `restrict`, "this record is still referred
to", today, with no probe and no verb (`errs/sqlerr/mysql.go:25`,
`errs/sqlerr/mariadb.go:22`, `errs/codes.go:71`).

On **PostgreSQL and SQLite** it is backwards. Both directions are one code —
SQLSTATE `23503` (`errs/sqlerr/postgres.go:19`) and SQLite extended `787`
(`errs/sqlerr/sqlite.go:23`, whose comment lists "foreign_key, restrict,
deferred_constraint" on one row) — and both map to `errs.CodeForeignKey`, whose
standard sentence is *"the record this refers to does not exist"*. That is the
inverse of what happened. The driver cannot tell them apart and says so: "The
direction has to come from the verb" (`errs/sqlerr/postgres.go:36`). The verb is
right there and nothing reads it. So on two of the four engines the copywriter's
sentence is stable, machine-readable and wrong — and a correct reference
implementation sits two files over.

(2) and (3) do not hold anywhere. `Delete` and `DeleteAll` never consult the
probe map: only `Save`, `SaveAll` and `Update` do
(`crud/decorators/faults/faults.go:227`, `:236`, `:246` against `:270-278`). The
verb name is nevertheless documented as valid in the option's own Go doc
(`crud/decorators/faults/probe.go:41-42`) and in `docs/modules/en/faults.md:55`
and `docs/modules/ru/faults.md:55`. A handler wired for `"Delete"` is still
*declared* (`crud/decorators/faults/probe.go:101-115`), so it can refuse at
start-up for a wrong catalog and then answer nothing — it looks alive. The
candidate set has no delete-restrict entries either: `restricting` reads
`OnUpdate` and never `OnDelete` (`crud/probe/plan.go:161-167`), and restrict
terms are dropped for anything that is not `modeUpdate` (`:198-201`).
**If not ready:** On MySQL and MariaDB they get the right code and no field. On
PostgreSQL they read `Violation.Source.Constraint` and write their own count
query per child table; on SQLite that field is empty and they have nothing to
branch on. Closing (1) is smaller than it looks — the verb decides which of two
codes the two conflating engines get — and closing (2) and (3) is a restrict
candidate keyed on `OnDelete`, a delete-shaped request, and first, either wiring
the three unimplemented verbs or removing them from the documented set in four
files.

### H-FAULTS-11 — A 200-row import says which rows failed
**Who:** a background job fleet importing a customer's CSV
**Wants:** row 17 and row 104 named, not "the batch failed"
**Story:** The importer calls `SaveAll` with 200 rows. Two carry emails that are
already registered and one of those two is also a duplicate of another row in
the same file.
**Must hold:**
1. Each violation carries the index of the row it belongs to.
2. Two rows of the same file colliding with each other are both reported.
3. It is off unless asked for, because a batch is where it costs most.
4. Asking for it cannot fail silently.
**Today:** 🟡 partial
**Evidence:** (1) `crud/probe/full.go:271-284` prefixes the row index and
`TestABulkWriteAttributesEachViolationToItsRow` pins it live
(`test/integration/probe_test.go:757`). (2) is the map with no statement —
`crud/probe/dup.go:34-68`, with `TestASingleRowWriteHasNoIntraPayloadDuplicates`
as the control. (3) `WithProbe` deliberately does not reach the bulk verbs
(`crud/decorators/faults/probe.go:34-39`). (4) fails:
`faults.WithProbeFor("SaveAll", h)` is the whole mechanism, and the option
validates nothing — `s.set(op, h)` (`crud/decorators/faults/probe.go:47`)
accepts `"saveall"`, `"SaveALL"` or `"Sabe All"` and no-ops. There is a test
that every verb is *decorated* (`TestEveryVerbIsDecorated`,
`crud/decorators/faults/faults_test.go:264`, which reads `crud.Core`'s method
set rather than a written list) and none that every documented verb is
*probeable*, which is how H-FAULTS-10's blocker shipped. Also: 200 rows against
a 50-row cap means three quarters of the file is unexamined; the answer says so
via `Partial`, which is H-FAULTS-04's mechanism, not a second one.
**If not ready:** They raise `WithMaxRows` and hope, or chunk the import to 50.
The verb name has to become something a misspelling cannot survive — see
*The DX this should have* for why a defined string type is not that.

### H-FAULTS-12 — Two databases, and one database with two schemas
**Who:** a team whose main database and analytics database both have a `users`
**Wants:** each repository consulting the schema of the database it writes to
**Story:** They hold two pools. They load a catalog for each and want to be sure
a violation on the analytics database is never named from the main one's schema.
Separately, their PostgreSQL has an `archive` schema carrying its own `users`.
**Must hold:**
1. Two handles get two catalogs, and a source built over a handle finds the
   handle's catalog rather than a new one.
2. A bare table name resolves to what the *connection* would have resolved it
   to, not to whichever schema sorts first.
3. Getting this wrong is a start-up failure, not a wrong answer.
**Today:** ✅ ready
**Evidence:** (1) `catalog.Set` keys on `crud.KeyOf` and compares with
`crud.SameDataSource` — the same identity rule `crud.WithExecutorFor` uses,
reused rather than restated (`crud/catalog/set.go:46-80`). The suite covers a
`ReadWrite` pair sharing its primary's catalog
(`TestAReadWritePairAndItsPrimaryShareOneCatalog`,
`crud/catalog/set_test.go:146`), two pairs over unidentified primaries *not*
colliding (`:201`) and two goroutines racing to declare (`:91`), and
`TestOneSetHoldsFourLiveDatabasesWithoutMergingThem` runs it against four live
engines (`test/integration/catalog_test.go:986`). (2) holds because every
PostgreSQL statement is scoped by `pg_table_is_visible`
(`crud/catalog/postgres.go:38`, `:68`, `:106`) — the server's own answer to
"what does this bare name mean here" — with the resolved schema recorded
(`test/integration/catalog_test.go:691`). (3) An uncomparable handle is refused
rather than stored (`crud/catalog/set.go:47-51`).
**If not ready:** — with one boundary that is [[D-041]] working as decided and
is written down nowhere a consumer looks. The catalog resolves **once**, on the
connection it loaded from. A schema-per-tenant deployment that issues
`SET search_path TO tenant_42` per request gets one tenant's constraint list for
the life of the process. That is not a defect; nothing warns the person who
wires it. The request-time twin of the same problem is H-FAULTS-09.

### H-FAULTS-13 — What the catalog costs at boot
**Who:** the platform engineer who owns the readiness probe
**Wants:** to know what `catalog.Load` reads, how long it may take, and whether
two hundred pods rolling at once will notice
**Story:** Their production PostgreSQL carries a few thousand tables, most of
them a legacy application's. They bind twelve models. Start-up gets slower and
they want to know by how much and whether they can bound it.
**Must hold:**
1. What is read is nameable, and ideally narrowable.
2. There is a bound on how long the read may take.
3. The permission it needs is stated.
**Today:** 🟡 partial
**Evidence:** (1) is nameable and not narrowable. `Load` reads every table the
connection can see: the PostgreSQL statements are scoped by `relkind IN ('r','p')`,
`pg_table_is_visible` and a `pg_catalog`/`information_schema` exclusion and
nothing else (`crud/catalog/postgres.go:36-40`). There is no table list, no
schema list and no model-derived filter, and `Load`'s doc says so on purpose:
"It takes no options" (`crud/catalog/load.go:25`). Three statements per handle
(columns, constraints, and the backend probe), all of it resident in memory for
the process's life — "Everything after this call is memory" (`:14`).

(2) is better than the round-1 draft of this file said. `Load` takes a context
and its doc names it as the bound — "the caller's context is the only bound"
(`crud/catalog/load.go:27`) — so a deadline is available. What is missing is
that no guide or module doc passes one, and neither does the shipped snippet in
either usage guide.

(3) is stated where it is most useful: `probe.declare`'s `ErrUnknownTable` text
tells the operator "An empty catalog reads exactly like this: check that the
connection may read the schema" (`crud/probe/declare.go:44-46`). But the grant
itself — schema read rights across every table in the database, not only the
twelve the app binds — appears in no doc.
**If not ready:** They time it in staging and hope production's table count is
similar. Closing (1) is an option on `Load` that takes the tables the process
will bind, which is a knob nobody has decided a default for — the reason `Load`
has none. Closing (2) and (3) is two sentences in `docs/modules/*/catalog.md`
and a `context.WithTimeout` in four snippets.

### H-FAULTS-14 — A rolling migration, in both directions
**Who:** anyone deploying without a maintenance window
**Wants:** the fleet to survive a constraint being added, renamed or dropped
while half of it is on the old release
**Story:** The migration lands. Half the pods booted before it. Writes keep
arriving.
**Must hold:**
1. A constraint name the catalog does not know does not become a permanent hole.
2. Looking it up again does not turn every failed write into an introspection
   pass.
3. Once it is known, the probe reports it.
4. A constraint that went *away* does not take the process down.
**Today:** ❌ missing
**Evidence:** (1) and (2) are built and tested well — `Reload` with a per-name
backoff from 1s to 5 minutes and a per-handle 1s floor
(`crud/catalog/reload.go:68-111`), with
`TestManyDistinctUnknownNamesDoNotReintrospectOnceEach` and
`TestAReloadThatFindsTheNameResetsTheBackoff`
(`crud/catalog/reload_test.go:126`, `:165`) as the two controls. **Nothing in
the library ever calls it** — `rg '\.Reload\(' --include='*.go'` outside
`crud/catalog` returns nothing — and both module docs sell it to consumers as a
feature they may reach for (`docs/modules/en/catalog.md:122-127`,
`docs/modules/ru/catalog.md:126-131`). Even if the consumer calls it themselves,
(3) still fails: `probe.Full.Declare` snapshots the table pointer and the whole
candidate list at `Bind` time (`crud/probe/declare.go:56-59`), and a `Reload`
swaps the snapshot underneath it (`crud/catalog/reload.go:88-89`) without
touching the bound handler. The probe is frozen at declaration until the process
restarts.

(4) is the direction nobody looked at, and it is the likelier one.
`faults.declare` panics — twice (`crud/decorators/faults/probe.go:94`, `:106`).
A probe declared against a table the catalog no longer knows, or a
`probe.Skip` naming a constraint a migration renamed, brings the whole process
down at `Bind`: every endpoint, not that model alone, and on every pod as it
rolls. There is no documented way to degrade to `probe.Simple` instead of dying
and no guidance on migration ordering.
**If not ready:** They restart the fleet after every additive migration and
order every renaming one so the code deploy lands first. Closing (3) means the
probe re-deriving its candidates from the current snapshot rather than from a
copy taken at boot. Closing the *reload* is not `sqlfault`'s job:
`errs.Classifier.Classify` takes no context and is frozen into the contract
manifest (`errs/spi.go:15-17`, `errs/doc.go:51`), and `sqlfault.Columns` says of
itself "It takes no context and does no I/O. A signature that accepted one would
be a lazy loader, and a lazy loader cannot fail at start-up ([[D-041]])"
(`crud/sqlfault/catalog.go:17-18`). The layer that already has a context, the
fault and `f.Detail.Constraint` is `enrichProbed`
(`crud/decorators/faults/probe.go:157-166`). [[D-041]] separates `Reloader` from
`Catalog` precisely because reloading is not a lookup, so calling it from there
does not fight the decision.

### H-FAULTS-15 — The decorators listed in a sensible order, and the process would not boot
**Who:** anyone adding the probe to a chain that already has a gate and an audit
decorator
**Wants:** to list middlewares in the order that reads best
**Story:** They write `Users.Bind(db, faults.Enrich[User, int64](...),
security.Gate(policy))` because errors feel like an outer concern. The process
panics at `Bind`.
**Must hold:**
1. Getting it wrong fails at start-up, not at the first collision.
2. The failure says what to do about it.
3. There is a way out for a chain that genuinely cannot put it innermost.
**Today:** ✅ ready
**Evidence:** All three, and this is one of the better things here. `declare`
walks the chain with `crud.SourceOf` rather than asserting on the layer directly
below, and the comment records why: the assertion made the order decorators were
listed in decide whether the probe worked, because an interface embedded in a
struct promotes only its own method set — "a chain that was correct in every
other respect refused at start-up for a reason about Go's type system rather
than about the wiring" (`crud/decorators/faults/probe.go:85-91`, [[D-061]]). The
panic names `Next()`, `crud.Base` and `faults.WithSource`
(`:94-97`). `TestAProbeFindsItsSourceThroughADecoratorAboveTheRepository`
(`crud/decorators/faults/probe_test.go:286`) and
`TestADeclaredProbeWithNoReachableSourceRefusesAtBindTime` (`:308`) are the pair,
and `TestAProbeWithAnExplicitSourceBindsWhereverItSits` (`:329`) is the escape
hatch.
**If not ready:** — though the rule itself is a cost: it is bolded in both usage
guides and it is a rule nothing but a panic teaches. Whether the ideal is "still
innermost, but the panic names the fix" (which is what ships) or
"order-independent" is worth deciding once rather than per reader.

### H-FAULTS-16 — Break at start-up rather than at 3am
**Who:** a platform team that has been burned by a feature that was off in
production and nobody noticed
**Wants:** the process to refuse to boot when the model and the schema disagree
**Story:** They rename a column in the model and not in the database. Or they
point staging at a database the deploy user cannot introspect. They want the
deploy to fail, loudly, on the pod that rolled.
**Must hold:**
1. A model bound to a table the catalog does not know is a boot failure.
2. A primary key that does not identify a row is a boot failure.
3. A `Skip` naming a constraint that is not there is a boot failure.
4. An empty catalog — the shape a permissions problem takes — is caught rather
   than read as "this database has no constraints".
**Today:** ✅ ready, and it is the best thing in this subsystem
**Evidence:** All four are sentinels with reasons attached
(`crud/probe/declare.go:13-31`). `ErrUnknownTable` carries (4) in its own
comment: "a MySQL user with no information_schema grants reads zero rows rather
than being refused, so `Load` succeeds and the catalog is empty — and the first
declaration that names a table catches it", and the error text says so to the
operator: "An empty catalog reads exactly like this: check that the connection
may read the schema" (`crud/probe/declare.go:44-46`). `catalog.Load` refuses
rather than returning a half-built catalog beside a nil error
(`crud/catalog/load.go:20-24`), and `ErrUnknownDialect` fails at start-up
"because an empty catalog reads as 'this database has no constraint problems'"
(`crud/catalog/errors.go:28-31`). Pinned by
`TestADeclarationAgainstACatalogWithoutTheTableRefusesAtBindTime`
(`crud/decorators/faults/probe_test.go:338`) and its live twin
`TestADeclarationAgainstACatalogWithoutTheTableRefusesToStart`
(`test/integration/probe_test.go:809`).
**If not ready:** — with one honest qualification. `declare` returns immediately
when nothing is wired (`crud/decorators/faults/probe.go:80-82`), and
`faults.Enrich` never sees a catalog at all — grep for `catalog` in
`crud/decorators/faults/faults.go` comes back empty. So the entire refusal
machinery is reachable only by consumers who have already paid for
`probe.Full(cat)`. It is a property of the expensive wiring, not of the module.
Note also the tension with H-FAULTS-14 (4): the same panic that is this case's
guarantee is that case's outage.

### H-FAULTS-17 — The write is inside the ORM's transaction
**Who:** a team with a service layer that wraps several writes in one gorm or ent
transaction — the shape both usage guides teach
**Wants:** the same three violations they get outside a transaction
**Story:** The create runs inside an existing transaction they own through the
ORM. On PostgreSQL, the failed statement poisons it and the probe cannot run.
**Must hold:**
1. Inside a transaction it does not degrade into a wrong answer — the driver's
   own violation survives.
2. Where a savepoint can safely restore the transaction, the option exists.
3. A transaction vv does not own is never given a savepoint.
4. Which side of that a dialect is on is measured, not guessed from a name.
5. Which side of it a *deployment* is on is visible where the probe is wired,
   not only in a matrix in a decision doc.
**Today:** 🟡 partial
**Evidence:** (1) through (4) hold. The degrade is the early return at
`crud/probe/full.go:57-62`, whose comment says why — "Simple is the honest
answer" — gated on `runs` (`:85-96`). `WithSavepoints` is opt-in and costs two
statements on the happy path (`crud/probe/options.go:46-56`), a foreign
transaction is refused a savepoint whatever the mode says
(`crud/decorators/faults/probe.go:200-221`,
`TestAForeignTransactionIsNeverGivenASavepoint`), and the side comes from
`crud.StatementRollback` rather than the dialect's name
(`crud/probe/full.go:88-90`). `TestTheTransactionMatrix`
(`test/integration/probe_test.go:621`) walks the whole table live.

(5) is the one that fails, and it is the failure H-FAULTS-16's actor was hired
to prevent. The early return answers `req.Fault, nil`: `partial` is never
touched, `WithProbeError` is never called, and nothing logs. A team that wires
the probe, sees three violations in staging, then adopts a service-layer
transaction next quarter loses the feature on the deploy that introduced the
pattern, with no signal anywhere. H-FAULTS-05 calls the same silence a blocker
when the cause is an unprobeable constraint; the cause here is a transaction and
the standard has to be the same.

The other thing not covered: if the probe's *own* statement fails inside a
vv-owned transaction, the savepoint was already rolled back to and nothing rolls
back again (`crud/decorators/faults/probe.go:144-147`), so a caller who meant to
catch the conflict and carry on in the same transaction finds it poisoned. And
the wiring for this case is the worst in the subsystem: both guides push a second
executor into the context —
`crud.WithExecutor(ctx, crudsql.From(tx, crudsql.WithFaults(sqlfault.New("postgres"))))`
(`docs/usage-guides/ent.md:1325-1328`) — which needs the same catalog-backed
classifier as the main handle or writes inside the ORM's transaction lose their
columns, and nothing hands it one.
**If not ready:** They accept one violation inside transactions, or restructure
so the create is its own unit of work. Closing (5) is `Partial` on the early
return, or the probe-error callback with a sentinel — the answer already has the
wire shape for "this is incomplete".

### H-FAULTS-18 — The deferred constraint that fires at COMMIT
**Who:** a team with circular references or a bulk load, using
`DEFERRABLE INITIALLY DEFERRED`
**Wants:** the same coded, field-named 409 they get from an immediate constraint
**Story:** The writes all succeed. `tx.Commit()` fails with the duplicate.
**Must hold:**
1. The commit's refusal is classified like a statement's.
2. It reaches the same enrichment every other refusal does.
**Today:** 🟡 partial
**Evidence:** (1) holds, deliberately and with the failure written down:
`crudsql.Tx.Commit` classifies, because "returning it untouched made that one
shape of conflict a 500 while the immediate shape was a 409"
(`crud/adapter/crudsql/crudsql.go:197-201`). So a code and a 409 arrive.

(2) does not. `faults.Enrich` decorates `crud.Core`'s methods
(`crud/decorators/faults/faults.go:220-285`), and `Commit` is on `crud.Tx`, not
on `crud.Core` (`crud/executor.go:43-47`). A commit passes through no decorator,
so the column-to-model-field hop — the one thing this module exists to do —
never runs. The probe would have dropped the constraint anyway: `reproducible`
returns false for `Deferrable` (`crud/probe/plan.go:147-149`). The consumer sees
a coded 409 with no field, no `Approximate` and no violations, from a call site
this subsystem does not model at all.
**If not ready:** They classify the commit error themselves and map the
constraint name to a field by hand — which needs `Source.Constraint`, so it works
on PostgreSQL and not on SQLite, the other engine with deferrable constraints.
Closing it means the enricher reachable from a commit, which is a `crud.Tx`
question rather than a `faults` one, and it should be named as such rather than
left unmodelled.

### H-FAULTS-19 — The write that failed for a reason no constraint caused
**Who:** a team whose write path is contended enough to deadlock
**Wants:** a retryable failure to cost what it costs, and to say only what
happened
**Story:** Two transactions deadlock. PostgreSQL kills one with `40P01`. The
application's retry loop — which the `retryable` kind invites — sends it again.
**Must hold:**
1. The probe does not run for a failure it cannot explain.
2. A retryable fault does not acquire violations that had nothing to do with it.
**Today:** ❌ missing
**Evidence:** Neither holds, and the trigger is one missing condition.
`enrichProbed` gates on `errs.AsFault` and nothing else
(`crud/decorators/faults/probe.go:158-163`), and `full.Enrich`'s own guards are
`req.Fault == nil`, `f.meta`, `req.Meta`/`req.Source`/`len(req.Rows)` and `runs`
(`crud/probe/full.go:48-62`) — the fault's code and kind are never consulted.
PostgreSQL's `40P01`, `40001`, `55P03` and `25P02` all classify
(`errs/sqlerr/postgres.go:25-28`) and all carry `KindRetryable`
(`errs/codes.go:94-98`), and `Classify` builds a fault for any listed state
(`crud/sqlfault/classify.go:71-88`). So they are faults, and the probe fires.

The consequence has two halves. The up-to-800-subquery statement fires precisely
when the database is in lock contention, against the contended table, and the
retry loop multiplies it — a sharper version of H-FAULTS-20 (3) with a trigger
nothing names. And `merge` appends whatever it found to the retryable fault,
touching only `Violations` and `Partial` (`crud/probe/full.go:157-189`), so the
client can receive "try again" carrying a `unique` violation on `email` that had
nothing to do with why the write failed. `KindRetryable` outranks
`KindConflict` (`port/kind.go:76-91`), so the status stays right and the body
does not.
**If not ready:** Nothing — the consumer cannot see it happening. Closing it is
one condition in `enrichProbed`: probe only where the fault's kind is one the
probe can explain. That is `KindConflict` and `KindValidation`, and the list
belongs next to the gate rather than in a comment.

### H-FAULTS-20 — What this costs when it is quiet, and what it costs when it is not
**Who:** a team sizing this for a hot write path before a launch
**Wants:** a number for both paths
**Story:** They are told the probe issues an extra statement. They ask: on every
write, or only failed ones? And what happens when something upstream starts
retrying?
**Must hold:**
1. A successful write issues no extra statement.
2. The cost of a refused write is bounded and nameable.
3. A burst of refused writes cannot take the database with it.
**Today:** 🟡 partial
**Evidence:** (1) holds and is a selling point the docs leave on the table.
`enricher.probed` calls the handler only after `run(ctx)` returns
(`crud/decorators/faults/probe.go:131-155`), and `enrichProbed` returns
immediately on `err == nil` (`:158-160`). The one exception is stated where it
lives: `WithSavepoints` is "the only part of this package that touches the happy
path, which is why it is opt-in" (`crud/probe/options.go:46-53`).

(2) is nameable: one SQL statement, at most 16 constraints × 50 rows = 800
`EXISTS` subqueries in one `SELECT`, under a 250ms timeout
(`crud/probe/options.go:11-23`, `crud/probe/plan.go:180-220`). That is a real
number and it belongs in the module doc; today the reader derives it from three
constants.

(3) does not hold. There is no aggregate limit, no breaker and no shed — only
per-request caps. A retry loop, a signup bot, or a downstream service replaying
a poison batch turns each refusal into a second query, on the path that is
already hot precisely because something is wrong. H-FAULTS-19 is the version of
this that fires without a single constraint being involved. Nothing counts
probes, nothing sheds them under load, and nothing exposes a metric to alert on.
**If not ready:** They wrap the repository and count refusals themselves.
Closing (3) is a ceiling on concurrent probes with a documented behaviour when
it is hit — which is `Partial`, an answer this subsystem already has.

### H-FAULTS-21 — Nothing internal reaches the public API
**Who:** anyone with an endpoint on the open internet
**Wants:** no constraint name, no table, no SQLSTATE, no driver sentence in any
body, at any status — and no field belonging to a model that had nothing to do
with the write
**Story:** Security review asks what a 409 body can contain.
**Must hold:**
1. The offending value is not in the body unless someone asked for it.
2. A code the vocabulary does not know does not become a 500 claiming to be a
   conflict, or a 409 carrying the driver's prose.
3. A column name from another table never becomes this model's field.
**Today:** ✅ ready
**Evidence:** (1) `WithValues` is off by default with the oracle argument
written into the option (`crud/probe/options.go:74-80`), and
`TestTheOffendingValueReachesTheBodyOnlyWhenAsked`
(`test/integration/probe_test.go:581`) pins both legs. (2) A fault is built only
when a code *and* its kind are both known, because `KindInternal` is the zero
value and a fault from an unwired vocabulary would claim 500 for a duplicate key
(`crud/sqlfault/classify.go:79-85`,
`TestAFaultIsBuiltOnlyWhenACodeAndItsKindAreKnown`);
`TestAFaultCarriesNothingTheDriverSaidInItsErrorText`
(`crud/sqlfault/classify_test.go:216`) is the one that would catch a regression.
(3) is the leg that is genuinely this module's: `resolvePath` refuses when the
driver's table does not fold to the model's
(`crud/decorators/faults/faults.go:160-168`) — "Two tables in one database have
a `name`, and translating this one would name a field of a model that had
nothing to do with the write" — and
`TestAProbeViolationNamingAnotherTableIsMarkedApproximate`
(`crud/decorators/faults/probe_test.go:231`) is the probe-side twin.

The broader guarantee — that `Violation.MarshalJSON` emits field, code and
message and nothing else, on a *value* receiver so a violation marshalled as a
map entry cannot bypass it (`errs/violation.go:73-111`, [[D-044]]) — is `errs`'
guarantee about any violation from any producer, and belongs to that sweep. It
is cited here, not counted here.
**If not ready:** —

### H-FAULTS-22 — Turning the probe on moved a 422 to a 409
**Who:** a front-end team whose client branches on the status
**Wants:** enabling a richer body not to change the status of a failure that
already worked
**Story:** Their signup handler treats 422 as "show field errors" and 409 as
"show the retry banner". They enable the probe for richer bodies. A subset of
signups that used to be 422 start arriving as 409, intermittently.
**Must hold:**
1. Adding violations to a fault does not change what the fault is.
2. If it can, the docs say so where the option is turned on.
**Today:** ❌ missing
**Evidence:** Neither holds and nothing in the tree names it. The status comes
from the resolved kind ([[D-049]]), and `KindOfWith` takes the *worst* kind
across the fault and every violation's code (`port/kind.go:32-47`), with
`KindConflict` ranked above `KindValidation` — "a collision is a fact about the
world the client cannot fix by editing its own payload" (`port/kind.go:61-101`).
`merge` appends the probe's violations to a copy of the driver's fault and
touches only `Violations` and `Partial` (`crud/probe/full.go:157-189`), never the
kind. So a payload whose *first* failure is a blank required field — 422,
`required` — and whose probe then finds a duplicate email renders 409. Which
constraint the engine reached first decides it, which is why it is intermittent.

The ranking itself is right and well argued. What is missing is that nothing
warns the person enabling the probe that a status they already ship can move,
and there is no option that says "add violations, keep the kind" — `CodeOnly`
drops the path, not the kind (`crud/probe/options.go:82-84`).
**If not ready:** They branch on `error_code` instead of the status, which means
rewriting the client's error handling as part of enabling a server option.
Closing the documentation half is one paragraph in `docs/modules/*/faults.md`
and one in both usage guides. Closing the behaviour half is a decision: either
the probe may not raise the kind, or it may and that is stated.

### H-FAULTS-23 — The violation whose field could not be worked out
**Who:** the author of a hand-written endpoint, without `cmd/vv -adapter`
**Wants:** to tell "this column maps to nothing in my model" from "the driver
named no column"
**Story:** A 409 comes back with a code and no field. They cannot tell which of
the two happened, and the two are different bugs.
**Must hold:**
1. A path that could not be resolved is marked, not invented.
2. The consumer can act on the mark.
3. "No field" means one thing on the wire.
**Today:** 🟡 partial
**Evidence:** (1) holds: `resolve` sets `v.Approximate = true` and leaves the
path nil rather than guessing (`crud/decorators/faults/faults.go:145-149`,
[[D-043]]), and the probe's own `path` does the same
(`crud/probe/full.go:271-283`). (2) holds *in Go* and nowhere else — the round-1
draft of this file overstated it. `Approximate` is an exported field on the
exported `Violation` (`errs/violation.go:66-70`), so a handler holding
`errs.AsFault(err).Violations` can branch on it, log it, or render its own body
from it. What no consumer can do is see it from a client: `MarshalJSON`
deliberately drops it (`errs/violation.go:73-76`), and no shipped layer logs it.
UC-017 guarantee 2 is explicitly "exact for a generated resource and best-effort
otherwise" (`docs/ai/usecases/modules/faults/UC-017-...md:101-109`), which makes
this the ordinary case for anyone who has not run `cmd/vv -adapter`.

(3) fails, and there are four situations behind one wire shape, not three. The
composite key that resolves to the empty path is deliberately *not* approximate
(H-FAULTS-03). The unresolvable column is approximate and invisible. The driver
that named no column never reaches `resolve` at all. And on MySQL, MariaDB and
SQLite there is a fourth: when the driver named no constraint, `same` folds by
code only when exactly one probe violation carries that code, "because with two,
there is no way to tell which of them the engine stopped at"
(`crud/probe/full.go:192-198`, pinned by
`TestAnUnnamedViolationIsNotFoldedIntoOneOfTwoCandidates`,
`crud/probe/full_test.go:600`). A payload that breaks two unique keys on those
three engines therefore renders **three** entries for two problems, one of them
field-less. The narrowing is deliberate and correct; nothing declares it.
**If not ready:** They run `cmd/vv -adapter` and get a total mapping, which is
the right answer and is a different subsystem. Short of that, they deduplicate
by code in the handler and lose one real violation when two keys genuinely broke.

### H-FAULTS-24 — Our own validation errors and the database's, in one body
**Who:** the same backend author, whose handler already rejects two fields
before the write
**Wants:** one response listing everything wrong with the form, in one shape,
in one order
**Story:** The handler checks the password length and the terms checkbox in Go,
finds two problems, and would still like to know about the taken email without a
second round trip.
**Must hold:**
1. A violation the application produced and one the probe produced have the same
   shape on the wire.
2. They can be merged into one fault without either side knowing about the other.
3. The order is deterministic whichever side produced more of them.
**Today:** ✅ ready, and this subsystem's docs never say where it lives
**Evidence:** All three hold and all three are `errs`'. A fault is additive
([[D-038]]) and `errs.Violation` is one type whoever built it; `Origin`
separates input from state so the envelope can group them, which is why
`sqlfault` writes `OriginState` explicitly rather than letting the zero value
stand (`crud/sqlfault/classify.go:80-84`). The probe's `merge` sorts the whole
list with `errs.SortViolations` over the fault it was handed
(`crud/probe/full.go:188`), so a fault that already carried the handler's two
violations comes back with all of them in one order — the probe does not need to
know they are there, and it does not remove them: `merge` copies
`req.Fault.Violations` first (`:158-160`). UC-017 guarantee 9 records the
property (`docs/ai/usecases/modules/faults/UC-017-...md:98`).
**If not ready:** — but the signposting is missing on both ends. Nothing in
`docs/modules/*/faults.md` or `probe`'s package doc says that a fault the
handler already built survives the probe, and the composition has no worked
example. The consumer's most likely wrong guess — that the probe replaces the
fault — is the one thing `merge`'s comment exists to deny, and the comment is in
the source.

### H-FAULTS-25 — The probe's failure reaches our request log
**Who:** whoever is on call
**Wants:** the probe error in their structured logger, with the trace id, so it
can be alerted on
**Story:** `partial: true` starts appearing in production. They want to know why
and which requests.
**Must hold:**
1. The failure reaches the application rather than being swallowed.
2. It can carry the request's own fields.
3. It never reaches the client.
**Today:** ❌ missing on (2)
**Evidence:** (1) and (3) hold: `WithProbeError` is the way out
(`crud/decorators/faults/probe.go:62-64`), the fault keeps its 409 and gains
`Partial` (`crud/decorators/faults/probe.go:167-177`), and
`TestAProbeFailureIsHandedToTheCallerAndNotToTheClient`
(`crud/decorators/faults/probe_test.go:452`) pins the pair. (2) does not: the
callback signature is `func(op string, err error)` — no context. The only place
it is called has one (`crud/decorators/faults/probe.go:170`, inside
`enrichProbed(ctx, …)`) and does not pass it. So the request's trace id, the
principal and the tenant are all unreachable, and the only thing left is a
process-wide logger. [[D-062]] exists to say the library must not do that, and
`port.Logger(ctx)` is named as the seam — yet both usage guides and both module
docs show `log.Printf` in this callback, because nothing else is available
(`docs/usage-guides/ent.md:1365`, `docs/usage-guides/gorm.md:1284`,
`docs/modules/en/faults.md:40-42`, `docs/modules/ru/faults.md:40-42`).
**If not ready:** They log without request fields, or stash a logger in a
closure per request and rebuild the repository per request, which nobody will
do. Closing it is one parameter — see *What it must not break* for the naming
question that comes with it.

### H-FAULTS-26 — Assert the three violations in a unit test
**Who:** the author of the handler that renders them
**Wants:** a test that a payload with three problems renders three fields, with
no database
**Story:** They already unit-test repositories with `crudtest`. They look for
the same thing here.
**Must hold:**
1. A schema can be described in Go and handed to the probe.
2. The write's own statement and the probe's can be told apart from a fake.
**Today:** 🟡 partial, and this sweep owns both halves
**Evidence:** (2) is the sharper half and is nobody else's. `crudtest.Recorder`
answers `Query` from one queue (`crud/crudtest/recorder.go:147-158`), so on a
dialect with `RETURNING` the write's own statement and the probe's arrive
through the same door in an order the test has to know. The library's own tests
write a datasource by hand rather than use the recorder and say why: "written
out rather than driven off `crudtest.Recorder` because these tests have to tell
the write's own statement from the probe's, and both arrive through `Query` on a
dialect with `RETURNING`" (`crud/decorators/faults/probe_test.go:56-60`).

(1) is possible and not supplied. `catalog.Catalog` is three methods and
`catalog.Table` / `Constraint` are plain exported structs
(`crud/catalog/catalog.go:13-19`, `:94-190`), so nothing stands between here and
an exported in-memory one. The library writes the fake twice for its own tests
(`crud/probe/fixture_test.go:47-135` with `Referrers`,
`crud/decorators/faults/probe_test.go:36-55` without) and exports neither. The
crudtest sweep looked at this and handed it here rather than keeping it, for a
structural reason that also decides where the fix may live:
`crud/crudtest` is TIER0 (`scripts/checks.sh:TIER0`) and `crud/catalog` is outside the
contract manifest under [[D-048]], so `make check-tiers` fails the moment
`crudtest` imports it (`docs/ai/usecases/modules/crudtest/Crudtest.md:256-269`).
**The in-memory catalog therefore cannot be exported from `crudtest`.** It goes
in `crud/catalog` itself or in a `catalogtest` package beside it.

Beware one false lead: `TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam`
(`crud/catalog/set_test.go:255`) reads as if this were closed. Driving
`catalog.Load` from a recorder means queueing three result sets in the loader's
exact order with the loader's exact column counts, which is what
`crud/catalog/fixture_test.go` does per dialect, saying of itself that "a
statement that gains a column breaks these rather than mis-scanning".
**If not ready:** roughly 45 lines of fake catalog and 40 of fake source, per
consumer, before the first assertion.

### H-FAULTS-27 — Our database is not one of the four
**Who:** a team on CockroachDB, or SQL Server, over `database/sql`
**Wants:** codes and fields for their engine, or a clear answer that they cannot
have them
**Story:** They wire `crudsql.Open`, hit a duplicate key, and get a 409 with no
code.
**Must hold:**
1. The refusal is explicit rather than a silent degrade.
2. There is a supported route for a fifth engine.
**Today:** ❌ missing
**Evidence:** (1) holds for the catalog and fails for the classifier, and the
round-1 draft of this file credited the wrong half. `catalog.Load` refuses with
`ErrUnknownDialect` rather than degrading, "because an empty catalog reads as
'this database has no constraint problems'" (`crud/catalog/errors.go:28-31`) —
but a consumer only reaches that if they paid for the probe. `sqlfault.New`
validates nothing: the four engine strings live in a doc comment, not in the
signature or a check (`crud/sqlfault/classify.go:39-52`), so `New("cockroach")`
constructs happily, boots happily, and answers false for every error forever —
`sqlerr.Classify` falls through to `return "", errs.Source{}, false` for an
unknown dialect (`errs/sqlerr/classify.go:32-42`), documented as deliberate at
`:24-27`. That degrade is pinned as the specification:
`TestAnUnknownDialectStillAnswersTheIntegrityGate`
(`crud/sqlfault/classify_test.go:287-306`) runs `New("cockroach")` and `New("")`
and asserts the sentinel with no code, with a control. It is a third place this
library declines to refuse at declaration, and the story's own symptom — "a 409
with no code" — is exactly what it produces.

(2) exists on paper and is exercised by nothing. `sqlfault.Columns` is
explicitly designed as a third-party SPI — "A third party can supply a schema
without importing catalog" (`crud/sqlfault/catalog.go:10-21`) — and no test,
example or doc walks a consumer through supplying their own `errs.Classifier`
plus their own `Columns`. A fifth dialect also silently disables the probe's
unique half on upserts, which is H-FAULTS-07 (2).
**If not ready:** They write a whole `errs.Classifier` from the shape of
`sqlfault`'s, with no worked example anywhere in the repository to copy. Worth
one page in the module doc naming the two interfaces and what each has to
answer — and one line in `New`'s signature or a `Bind`-time refusal, so an
unrecognised engine string fails where it is written.

### H-FAULTS-28 — pgx, and the option that quietly drops the typed reader
**Who:** the flagship PostgreSQL consumer, on `crudpgx`
**Wants:** the catalog wired in, without giving anything up
**Story:** They follow `crudpgx.WithFaults`' own doc comment and pass
`sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))`.
**Must hold:**
1. Adding a catalog does not remove anything the default had.
2. If it does, the consumer is told at the point they write it.
**Today:** ❌ missing
**Evidence:** Neither holds. `crudpgx`'s default classifier carries
`sqlfault.WithExtractor(sqlfault.ExtractorFunc(extract))`
(`crud/adapter/crudpgx/crudpgx.go:63-67`) — a typed reader of `*pgconn.PgError`
that exists, per its own comment, so that "a field pgx renames breaks the build
here, where the by-shape reader in sqlfault goes quietly blank"
(`crud/adapter/crudpgx/conflict.go:23-25`). `WithFaults` replaces the whole
classifier (`:60`), so the documented example at `:57-59` — which omits the
extractor — silently gives that up. Classification keeps working today because
`sqlfault.Extract` reads pgconn by shape, so the symptom is deferred to the next
pgx field rename, at which point every PostgreSQL 409 becomes a silent 500. That
is the same class of defect as H-FAULTS-01's: the documented way to add the
thing you want removes a thing you did not know you had.
**If not ready:** They read `crudpgx.faults` and copy the extractor into their
own call. Closing it is either an option that composes onto the default instead
of replacing it, or an example that shows the extractor.

## The DX this should have

### The call site

```go
// the shortest thing that works
src, cat, err := crudsql.Introspect(ctx, crudsql.Postgres(sqlDB))
if err != nil {
    return err // the schema could not be read; fail here, not on the first collision
}

users := Users.Bind(src, faults.Enrich[User, int64](faults.WithProbe(probe.Full(cat))))
```

The signature is
`func Introspect(ctx context.Context, db DB, opts ...sqlfault.Option) (DB, catalog.Catalog, error)`,
with `crudpgx.DB` for the pgx twin. It returns `DB` and not `crud.Source`,
because `DB` carries `Begin` and the exported `TxOptions` field
(`crud/adapter/crudsql/crudsql.go:138-144`) and narrowing to the interface would
make the short path a one-way door. The value returned is a copy, so `TxOptions`
must be set on it — `WithTxOptions` already returns a copy for the same reason
(`:174`) — and the doc comment has to say so. Identity survives either way:
`DataSource()` still answers the same `*sql.DB`, so `crud.WithExecutorFor` and
`catalog.Set` both still see one database.

Two lines and two imports beyond the adapter, against three lines and three
imports for today's probe route, and four lines and three imports for the
classifier route. The arithmetic on its own does not justify a new constructor,
and the round-1 draft of this file oversold it: the catalog is still a noun the
consumer holds, because `probe.Full(cat)` needs it. What the constructor actually
buys is one line, one import, a strictly better source — the driver's own violation gains
`WithColumns(FromCatalog(cat))`, which today's probe route does not have at all —
and the removal of the only step that is easy to get wrong: building the handle
twice, in the right order, with the engine string written a second time.

**Ownership is deliberately split.** The adapter package owns source adoption,
catalog loading, and the proposed `Introspect`/`WithFaultsFrom` assembly; this
Faults module owns optional enrichment and probe declaration over the source the
adapter returned. That is the one source/catalog-transfer owner shared with the
Adapters sweep: `faults.WithSource` remains only the opaque-chain escape hatch
and must not grow a second handle-construction path.

`Introspect` does I/O at startup but does not open or close the caller's database:
the caller owns the `*sql.DB`/pool lifetime, context, and the account's metadata
read grants. It returns catalog-load failure before any repository binds; a
service that cannot grant schema reads uses an explicit prebuilt catalog or does
not enable `Full`. The proposal must document that it queries schema metadata and
that the returned replacement source—not the pre-introspection handle—is the one
to bind.

### Turning one knob

```go
src, cat, err := crudsql.Introspect(ctx, crudsql.Postgres(sqlDB),
    sqlfault.WithCodes(ourVocabulary)) // an sqlfault.Option, forwarded to the classifier
if err != nil {
    return err
}

// One handler value, shared by every repository: Declare returns a copy
// (crud/probe/declare.go:56-59), so binding twenty models does not build twenty.
ph := probe.Full(cat,
    probe.WithScope(policy.Scope),      // every wired verb, not one of them
    probe.Require("users_email_key"))   // refuse at Bind if this key is unprobeable

errOpts := []faults.Option{
    faults.WithProbe(ph),                    // Save and Update
    faults.WithProbeFor(faults.SaveAll, ph), // …and the importer
    faults.WithProbeError(func(ctx context.Context, op string, err error) {
        port.Logger(ctx).Warn("probe failed", "op", op) // the request's own fields
    }),
}

users := Users.Bind(src, security.Gate(policy), faults.Enrich[User, int64](errOpts...))
```

Four changes from what ships, each named as a change rather than smuggled in.
`Introspect` takes `sqlfault.Option`s, so a vocabulary or an extractor is not a
reason to abandon the short path (H-FAULTS-28 is what happens when it is).
`faults.SaveAll` names the verb without a bare string — see below for why the
obvious spelling of that does not work. `probe.Require` is the inverse of
`probe.Skip` and closes the file's highest-severity finding (H-FAULTS-05).
`WithProbeError` keeps its name and gains a `context.Context`.

Hoisting `ph` is not cosmetic. It is what stops H-FAULTS-08's leak being
reintroduced by the snippet a reader copies: an earlier draft of this section
scoped `WithProbe` and left `WithProbeFor(SaveAll, probe.Full(cat))` unscoped on
the next line, which is the unnarrowed existence oracle on the verb that touches
the most rows. The deeper shape problem stays: the scope is a property of the
repository's gate and has to be re-attached to every handler value by hand. If
that is unacceptable, the fix is `faults.WithProbeScope(policy.Scope)` at the
enricher, applying to every wired handler — and that should be decided, not left
to whoever writes the twentieth resource.

### Why this shape

**Because the engine string is written twice today and nothing checks the second
one.** `crudsql.Postgres(sqlDB)` declares `"postgres"`
(`crud/adapter/crudsql/crudsql.go:159,164-166`) and then the consumer writes
`sqlfault.New("postgres")` again to reach `WithColumns`. Two declarations of one
fact is how they drift, and the drift is silent: a MariaDB handle declared
`"mysql"` in the second place moves a failed CHECK from 422 to 409 ([[D-019]]
difference 10b). There are two ways to remove the second declaration and both
are [[D-046]]-compatible. `Introspect` can read the declaration back off the
handle through `sqlfault.Classifier.Engine()` (`crud/sqlfault/classify.go:54-60`)
— reading back what the caller already said, not deriving it. Or it can use the
one *measured* engine string in the tree: `Introspect` loads the catalog before
it needs an engine, and `catalog.Catalog.Dialect()` distinguishes MariaDB from
MySQL with `SELECT VERSION()` (`crud/catalog/load.go:58-94`,
`crud/catalog/catalog.go:16-19`), which is exactly the route D-046's own forbid
list blesses: "A consumer who wants it measured has exactly one measurement in
the tree, `catalog.Catalog.Dialect()`, and may wire it through
`crudsql.WithFaults`" (`docs/ai/decisions/D-046-...md:130-132`). The measured
route is better and should be the default; the readback is what makes
`crudsql.Open` and `crudsql.Source` usable too, since those name a dialect and
no engine. **This is the same accessor the adapters sweep needs for its own
`Adopt`** (`docs/ai/usecases/modules/adapters/Adapters.md:357-361`), so it should
land on its own, ahead of anything else here.

**Because it must say where the catalog is kept.** [[D-041]] forbids a
package-level catalog, so `Introspect` cannot hold a `catalog.Set` of its own,
and calling `catalog.Load` twice over one handle is the double introspection the
decision exists to prevent. The answer is that `Introspect` calls `Load` once and
hands the catalog back, and the *consumer's* `catalog.Set` — if they have two
databases — is where it goes, exactly as today. Two `Introspect` calls over one
handle build two catalogs, and that is a wiring error the consumer can see,
because both return values are in front of them. A `*catalog.Set` parameter was
considered and rejected: it adds a noun to the call site for a case only
multi-database consumers have, and `catalog.Set` already refuses an uncomparable
handle on its own (`crud/catalog/set.go:47-51`).

**Because the constructor cannot live in `faults`.** `crud/decorators/faults`
declares `Depends on: crud, errs, probe` (`docs/modules/en/faults.md:7`), and a
constructor that returns a `crud.Source` has to build one — which means
importing `catalog`, `sqlfault` and an adapter. Worse, the root module cannot
import `crudpgx`, so anything in `faults` serves `database/sql` only and leaves
out the flagship PostgreSQL path entirely ([[D-033]]). Per-adapter is where it
belongs, and each adapter already imports `sqlfault`, which already imports
`catalog` — so nothing here adds a dependency.

**Because two adapters must not implement one assembly twice.** Everything but
the final handle rebuild is engine-agnostic once the catalog exists:
`cat.Dialect()` supplies the engine string and
`sqlfault.New(engine, sqlfault.WithColumns(sqlfault.FromCatalog(cat)))` the
classifier. That belongs in one exported helper both adapters call — this
section spent a paragraph objecting to two declarations of one fact and must not
ship two implementations of one assembly.

**Because it must say what happens to the handle you passed in.** `Introspect`
returns a *replacement*: `crudsql.WithFaults` is a construction-time option and
`Executor.faults` is unexported with no mutator
(`crud/adapter/crudsql/crudsql.go:43-46`), so there is no way to attach a
catalog to a source that already exists. Binding a repository to the original is
a silent downgrade to the catalog-less classifier — H-FAULTS-01's failure,
reintroduced by the fix for it. A doc comment is not enough to hold that, because
a doc comment is what already failed in the guides. Make it structural: either
`Introspect` takes the ingredients rather than a built handle
(`crudsql.Introspect(ctx, crudsql.Postgres, sqlDB, opts...)`), so there is no
original to bind by mistake, or `catalog.Set` refuses a second source over the
same datasource carrying a different classifier — it already compares handles
with `crud.SameDataSource` (`crud/catalog/set.go:47-57`), so the check exists
and only the refusal is missing.

**About `crudsql.From`, which is not the case it looks like.** The
joined-ORM-transaction path (H-FAULTS-17) is the wiring most in need of help, and
`Introspect` does not reach it: `From` returns an `Executor`, which has no
`Dialect()` and so is not a `crud.Source` (`crud/adapter/crudsql/crudsql.go:75-77`,
`crud/executor.go:57-60`). It cannot be an argument here at all. What that call
site needs is a different, smaller thing — proposed `crudsql.WithFaultsFrom(src)`, an
option that copies the classifier the main handle already holds, so the
per-request line becomes `crudsql.From(tx, crudsql.WithFaultsFrom(src))`: one
noun, no second engine string, and the columns ORM-transaction writes lose today
are kept. Since `Introspect` lives in package `crudsql` it can read the
unexported field directly and needs no exported accessor for its own sake; the
accessor is worth exporting for this option and for the adapters sweep's
`Adopt`, not for the constructor.

`WithFaultsFrom` transfers **only** immutable classifier configuration; it does
not turn the foreign transaction into a `crud.Source`, copy `Begin`, or install a
probe. The caller still captures that transaction with
`crud.WithExecutorFor(ctx, src, tx)` for the repository call, so `Full` selects
the transaction executor already in context (`crud/probe/full.go:83-95,119-126`).
That makes transaction propagation explicit and avoids a second source whose
identity could drift from the write. A foreign transaction remains in Full's
documented advisory/no-savepoint branch, not an enforcement bypass.

**Because the six-line version is where the field goes missing.** A consumer who
stops after `faults.Enrich[User, int64]()` — which both module docs and the
README present as sufficient — gets a code and no field for every unique
violation on every engine. The shortest correct path should also be the shortest
path.

**On option vocabularies.** One expression here carries four: `crudsql.Option`
inside `Postgres(...)`, `sqlfault.Option` inside `Introspect(...)`,
`probe.Option` inside `Full(...)`, `faults.Option` inside `Enrich(...)` — and
`sqlfault.WithCodes` and `crudsql.WithFaults` sit two tokens apart doing related
things. The variadic on `Introspect` is `sqlfault.Option` because what it builds
is a classifier, and that has to be said in the doc comment in one sentence
rather than left to be guessed. If it cannot be said in one sentence, the
alternative is `crudsql.Option` plus a `crudsql.Faults(sqlfault.Option...)`
forwarder, so each call site has a single vocabulary.

**Two things this shape does not fix, and should not pretend to.** The tenant
scope still has to be written twice — `security.Gate(policy)` and
`probe.WithScope(policy.Scope)` — and nothing correlates them (H-FAULTS-08). The
refusal that would close it cannot live here: `crud.Chain` applies
innermost-first (`crud/repo.go:110-117`), so `security.Gate` wraps the enricher
and reaches it through `Next()`, while `faults` cannot walk up. And both type
parameters are still spelled at every call site, because Go cannot infer them
from `Bind`'s parameter position — the module doc already apologises for it
(`docs/modules/en/faults.md:27-28`). `Blueprint[M, ID, U]` carries both, so a
method (`Users.Enrich(ph)` returning a `crud.Middleware[M, ID]`) would drop the
brackets from every one. That is a `sqlrepo` change and belongs to whichever
sweep owns `Blueprint`.

**What the alternatives cost.** A package-level default catalog would delete
every line of this and is exactly [[D-041]]'s forbid — right in every
single-database test, wrong in the deployment that matters. A copy-returning
`Classifier.WithColumns(cols)` was suggested in review and does not work: the
executor holds the classifier it was given, so a copy made afterwards reaches
nothing, and a *mutating* setter would be a write to a value another object is
already reading through `Classify`. A constructor that panics instead of
returning an error would fit the house style (`sqlrepo.Define` and
`faults.declare` both panic) but hides I/O behind a call that reads like a
struct literal; `Load` already returns an error and this should keep it. The
general sweep's `vvkit.Errors(cat)` (`docs/ai/usecases/general/General.md:390-399`)
is a layer above this, not a competitor: it takes an already-loaded catalog, so
it composes with `Introspect` rather than replacing it. The adapters sweep's
round-1 `crudsql.WithCatalog(ctx)` was withdrawn there and this constructor
adopted in its place (`Adapters.md:344-350`), so there is one proposal, not
three.

### What it must not break

- [[D-046]] — the engine is declared, never derived. `Introspect` either reads
  back a declaration the caller made or uses `catalog.Catalog.Dialect()`, the one
  measurement the decision's own forbid list names. It must never fall through to
  `Dialect.Name()`, which answers `"mysql"` for MariaDB, and it must refuse
  rather than guess when neither is available.
- [[D-041]] — the catalog is per physical handle and never a package-level
  variable. `Introspect` holds no `Set`, loads once, and hands the catalog to the
  caller. Reloading is separate from lookup in that decision, which is why moving
  the `Reload` call into `enrichProbed` (H-FAULTS-14) is not a challenge to it:
  `enrichProbed` has a context because it is on the request path, and `Columns`
  still has none.
- [[D-042]] — the probe is advisory, and it stays **off** until named. An
  earlier draft of this design had the constructor turn the probe on for
  single-row writes by default, on the grounds that it is what everyone wants.
  That is wrong and it was withdrawn: the probe is an existence oracle,
  `README.md:1218` promises that none of this is on by default, and a default
  that widens a disclosure is not a convenience. Advisory also has to start
  meaning *side-effect free*, which H-FAULTS-17 and H-FAULTS-19 say it is not.
- [[D-030]] — `security.Gate` is enforcement and must decide every `crud.Core`
  verb before a statement; faults enrichment is advisory response decoration
  after an already-classified refusal. `WithProbe`/`WithProbeFor` may add detail
  or `Partial`, never authorise a write, recover a rejected row, or substitute
  for the gate's per-verb obligation. A new write verb therefore needs two
  explicit decisions: D-030 coverage in `security.Gate`, and whether the
  advisory probe is worth wiring for that verb.
- [[D-021]] — refuse at declaration. This library breaks its own rule in three
  places, not two: `WithProbeFor` accepting an unrecognised verb name,
  `reproducible` dropping a constraint in silence (H-FAULTS-05, H-FAULTS-07), and
  `sqlfault.New` accepting any engine string (H-FAULTS-27). **A defined string
  type does not close the first one** — `type Verb string` still accepts an
  untyped constant, so `faults.WithProbeFor("Sabe All", h)` would compile and
  no-op exactly as it does today, the way `errs.Code` is called with bare
  literals throughout this repository (`errs/code.go:11`, `errs/build_test.go:19`).
  The closure is a declaration-time refusal against a *derived* set: the verbs
  `enrichProbed` reaches (`crud/decorators/faults/faults.go:227,236,246`), read by
  the same reflection `TestEveryVerbIsDecorated`
  (`crud/decorators/faults/faults_test.go:264`) already uses for the sibling
  problem. That closes the typo and the three dead verbs at once, and makes the
  delete work an additive feature rather than a broken promise kept alive. It
  also decides `probe.Require`'s symmetry: a `Require` naming a reproducible
  constraint is a no-op, one naming an unprobeable constraint is a `Bind`
  refusal — the inverse of `Skip`.
- [[D-049]] — the kind decides the status. Nothing proposed here changes that,
  and H-FAULTS-22 is a request to *state* the consequence rather than to alter
  it. If the decision is that the probe may not raise a fault's kind, it belongs
  in D-049 rather than in `probe`.
- [[D-062]] — the library logs through the caller's logger. Adding `ctx` to the
  probe-error callback is not a challenge to it; it is the only way a consumer
  can honour it. The signature change is breaking, it stops four snippets in this
  repository's own docs compiling (`docs/usage-guides/ent.md:1365`,
  `gorm.md:1284`, `docs/modules/en/faults.md:40-42`, `ru/faults.md:40-42`), and
  it should land before the tag with the old form deleted rather than shimmed —
  two callbacks doing one job is how the wrong one gets copied.
- [[D-035]] — a consumer imports several of these packages in one file, so a
  name must not collide across them. This is why the round-1 draft's
  `faults.Detail` is withdrawn: `errs.Detail` is an exported struct on every
  fault and a frozen contract member (`errs/fault.go:17-28`, `errs/doc.go:49`),
  and `det.Probe` reading next to `f.Detail.Constraint` in one function is a
  second meaning for one word.
- [[D-043]] — one hop per layer. This is what keeps the constraint→code map out
  of `sqlfault` (see *Contested*): that layer has no `crud.Meta` and no view of
  the request, and a constraint name deciding a code there would be a hop it does
  not own.
- [[D-061]] — a wrapper forwards what it wraps. The correlation check
  H-FAULTS-08 (3) needs must be an optional interface the enricher implements
  and `security.Gate` finds by walking `Next()`
  (`crud/decorators/security/security.go:176`) — something shaped like
  `interface{ ProbeIsScoped() bool }`. Not a type assertion, and not
  `security` importing `faults`: one decorator knowing another's package is how
  the layering the rest of this file is careful about comes apart.
- No challenge is intended to [[D-044]] (nothing internal in a payload) or
  [[D-014]] (deterministic output). Nothing above touches them.

## DX verdict

*Distance* measures keystrokes from the ideal. It is not severity — the blocker
table measures what happens when the consumer gets it wrong, and the two diverge
where the mechanism is one option and the default is the dangerous one.

| What the ideal asks for | Today | Distance |
|---|---|---|
| The 409 names the field for a collision | 3 lines via the probe route, which works on all four engines; the documented "minimum" gets a code and nothing else | small |
| The 409 names the field for a blank required field | free on PostgreSQL, from the one-liner; not available at any length on the other three | large |
| The 409 names the field *without* the probe | 5 lines, the handle built twice, the engine named twice — and PostgreSQL-only, because `fill` needs a table and a constraint the other three drivers never supply | large |
| A composite unique names a field | not available at any length; the empty path is the designed answer | large |
| Every violation for one payload | 4 lines once, 1 per repository. Genuinely short | small |
| Know which of my constraints will never be probed | not expressible; the drop is silent and the answer says `partial: false` | large |
| An upsert that reports only what its conflict clause did not absorb | free on PostgreSQL and SQLite; silently no unique terms at all on a dialect that does not implement `UpsertScope` | large |
| Edit a row without it colliding with itself | free, and the only guarantee in the file nothing tells the consumer about | none |
| Narrow the probe to the tenant | one option per handler value, typechecking against `policy.Scope` — reaching unique terms only, with `Skip` as the only control for the other two | small |
| Narrow the probe when the boundary is RLS | not expressible: there is no predicate to pass, and session state does not follow the pooled connection | large |
| Probe one more verb | one option — and a misspelt verb name is a silent no-op | small |
| Probe a delete | not available at any length; the documented option does nothing, and on PostgreSQL and SQLite the code that does arrive says the opposite of what happened | large |
| Probe error in the request log | not expressible: the callback has no context | large |
| Keep the status a client already branches on | not expressible: the probe's violations can raise the fault's kind and there is no option that says otherwise | large |
| Merge my handler's violations with the database's | free, ordered, and documented in neither this subsystem's docs nor its package doc | none |
| The sentence under the form field | reachable through `errs`' message ladder, and nothing in this subsystem's docs says that is where it lives | small |
| Unit-test the rendered violations | ~85 lines of fakes before the first assertion, and the in-memory catalog cannot live in `crudtest` | large |
| Refuse at start-up when schema and model disagree | precise, well-worded, and reachable only from the wiring that already loaded a catalog | small |
| Refuse at start-up on an engine string nobody supports | not available: `sqlfault.New` takes any string and degrades silently forever | large |
| Decorators in the right order | one panic that names the fix, plus `WithSource` as the way out | none |
| Two databases without cross-contamination | `catalog.Set`, one call each | none |
| Costs nothing on the happy path | true, and stated in exactly one option's doc comment | none |
| Know what a refused write costs, and cap it | the per-request number is derivable (1 statement, ≤ 16 × 50 = 800 `EXISTS` subqueries, 250ms) and written down nowhere; nothing bounds the aggregate, and the probe also fires on deadlocks | large |
| Bound what the catalog reads at boot | not expressible: no table filter, every visible table, for the process's life | large |
| The same violations inside the ORM's transaction | one violation on PostgreSQL, with no `Partial`, no callback and no log to say the feature is off | large |
| A violation from a deferred constraint | a coded 409 with no field: a commit passes through no decorator | large |
| Catch the conflict and carry on in the same transaction | the savepoint is spent and the transaction is poisoned | large |
| Add a catalog on pgx without giving anything up | replaces the whole classifier, silently dropping the typed pgconn reader | large |
| Tell "no field by design" from "no field, we gave up" | the flag is an exported Go field and is dropped from every body; four situations share one wire shape | small |
| Survive a rolling migration | restart the fleet; the rename direction panics every pod | large |
| A fifth engine | degraded silently, not refused — and with no worked example of the supported route | large |

**Overall:** The middle of this subsystem feels like the ideal and then some —
the probe's plan, the caps with their reasons, the transaction matrix, the
self-collision guard and the declaration-time refusals are the work of someone
who had been bitten and wrote down why. The edges do not, and they fail in four
distinct ways. Some knobs are present and cost one option (`WithScope`, `Skip`,
`CodeOnly`, `WithSavepoints`, `WithSource` — all excellent). Some are present,
documented, and connected to nothing (`WithProbeFor("Delete")`,
`catalog.Reloader`), which is worse than a missing feature because the consumer
stops looking. Some narrowings are correct, deliberate, tested — and invisible:
the composite key that names no field, the partial index that is never probed,
the upsert on an unknown dialect, the foreign-key term that ignores the scope,
the unfolded violation on the three engines that name no constraint. And one
thing reaches further than it was asked to: the probe fires on failures no
constraint caused, and the violations it appends can raise a 422 to a 409. Each
is defensible on its own; together they mean a consumer can wire this exactly as
documented and get an answer that is quietly smaller — or quietly different —
from the one they think they are reading. The on-ramp is inverted on top of that:
the cheapest wiring is the one that does not deliver the thing everybody came for,
and none of the nine runnable examples wires any of it (`rg -l
"sqlfault|WithFaults|catalog\." _examples/` returns nothing; the adapters sweep
counts it at `docs/ai/usecases/modules/adapters/Adapters.md:165`, so it is one
gap and not two). The correct wiring has no working call site in this repository
to copy from, and the library's own integration harness builds the source twice
around `catalog.Load` (`test/integration/probe_test.go:102-120`) — the
doubled-handle problem, demonstrated by the authors.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `WithProbeFor` accepts `"Delete"`, `"DeleteAll"` and `"UpdateAll"` — documented in its own Go doc and in `docs/modules/en/faults.md:55` and `ru/faults.md:55` — and those three verbs never read the probe map | blocker | A wired option that declares successfully, can refuse at start-up, and then answers nothing is the failure this subsystem exists to prevent, shipped inside it |
| 2 | `faults.Enrich[T, ID]()` alone names no field for a **unique** violation on any of the four engines, and `docs/modules/en/faults.md:25`, `ru/faults.md:25` and `README.md:1083-1090` all say it does | blocker | It is the first thing every consumer wires, and the claim is false at the exact moment they check it. The honest sentence is narrower: the minimum names the field where the driver named a column, which on PostgreSQL is `NOT NULL` and never a unique key |
| 3 | A constraint the probe cannot replay — partial, expression-keyed, prefix, deferrable, a NULL key part, a key column the insert did not write — is dropped silently, the answer says `partial: false`, and nothing refuses at declaration | blocker | `UNIQUE (email) WHERE deleted_at IS NULL` is the default soft-delete schema; the consumer's most important constraint produces no term and the response presents itself as complete. It also contradicts UC-017 guarantee 6, which is marked `yes` — one of the two has to move |
| 4 | Turning the probe on can raise a fault's kind, moving a 422 to a 409 for a payload whose first failure was `required` | blocker | It changes the status of a failure that already worked, intermittently, depending on which constraint the engine reached first — and no option, doc or test names it |
| 5 | The probe runs for any classified failure, including `40P01`, `40001`, `55P03` and `25P02`, and appends whatever it finds to a retryable fault | serious | The 800-subquery statement fires exactly when the database is in lock contention, the retry loop multiplies it, and the client gets "try again" carrying an unrelated `unique` violation |
| 6 | An upsert on a dialect that does not implement `crud.UpsertScope` drops every unique term, issues no statement, and reports `partial: false` | serious | The default is "anything unknown swallows every unique key", so a fifth engine turns the probe's unique half off for the verb a sync job uses on every row |
| 7 | `UNIQUE (tenant_id, email)` resolves to an empty path and is deliberately not marked approximate | serious | It is the ordinary unique key in a tenanted product, so the headline promise is unreachable for a large share of consumers, and "no field" now means four different things on the wire |
| 8 | A probe wired without `WithScope` under a `security.Gate` is a cross-tenant existence oracle, and nothing at declaration correlates the two; `WithScope` narrows unique terms only | serious | The short wiring is the leaking one, the full wiring is still partly leaking, and the chain order makes the check undetectable from inside `faults` |
| 9 | Inside a foreign transaction the probe returns the driver's fault with no `Partial`, no `WithProbeError` call and no log | serious | The service-layer transaction is the shape both usage guides teach; a team loses the feature on the deploy that adopts it, with no signal anywhere |
| 10 | `WithProbeFor` validates no verb name; a typo is a silent no-op, and a defined string type does not fix it | serious | `probe.Skip` refuses an unknown constraint name for precisely this argument; the inconsistency turns a control off on the deploy that introduced it |
| 11 | `WithProbeError` takes no context, so a probe failure cannot carry the request's own fields | serious | [[D-062]] names `port.Logger(ctx)` as the only seam, and this callback cannot reach it — four of this repository's own snippets fall back to `log.Printf` |
| 12 | On PostgreSQL and SQLite a delete blocked by children comes back `foreign_key` — "the record this refers to does not exist" — the inverse of what happened | serious | MySQL and MariaDB already answer `restrict` from the driver, so the copywriter's sentence is right on two engines and backwards on two, and the code is stable enough that nobody notices |
| 13 | A violation from a `DEFERRABLE INITIALLY DEFERRED` constraint arrives from `Commit`, which passes through no decorator | serious | The column-to-model-field hop — the one thing this module exists to do — never runs, and the probe would have dropped the constraint anyway |
| 14 | `catalog.Reloader` is called by nothing, `probe.Full` freezes its candidates at `Declare` so a reload cannot reach it, and both module docs sell it as a feature | serious | The rolling-migration story is built, tested, advertised and unreachable; the fleet must restart |
| 15 | A migration that renames or drops a constraint panics `faults.declare` at `Bind`, taking every endpoint on every pod that rolls | serious | It is the likelier migration direction, there is no documented degrade to `probe.Simple`, and the symptom is CrashLoopBackOff rather than a bad response |
| 16 | `crudpgx.WithFaults` replaces the whole classifier, so the documented way to add a catalog drops `sqlfault.WithExtractor` and its typed pgconn reader | serious | Classification survives on the by-shape reader today; the next pgx field rename turns every PostgreSQL 409 into a silent 500, which is the failure the extractor exists to prevent |
| 17 | `sqlfault.New` accepts any engine string and degrades to "no code, forever", with the degrade pinned as the specification | sharp edge | It is the third place this library breaks its own refuse-at-declaration rule, and it produces exactly the symptom H-FAULTS-27's story reports |
| 18 | On MySQL, MariaDB and SQLite, two unique keys broken by one payload leave the driver's own violation unfolded | sharp edge | The client renders three entries for two problems and one of them names no field; the narrowing is deliberate and nothing declares it |
| 19 | A probe statement that fails inside a vv-owned transaction leaves it poisoned, with the savepoint already spent | sharp edge | "Advisory" reads as "side-effect free" and here it is not |
| 20 | No aggregate bound on probe statements: a burst of refused writes issues one extra 800-subquery statement each, with no breaker and no metric | sharp edge | The failure path is the one already hot when something is wrong, and the only per-request control is a 250ms timeout |
| 21 | The probe's statement runs on a pooled connection outside a transaction, so `SET search_path`, RLS set with `SET LOCAL` and temp tables do not follow it | sharp edge | An RLS-based tenant boundary has no `WithScope` predicate to hand over, and the probe's narrowing becomes whichever connection it landed on |
| 22 | `catalog.Load` reads every table the connection can see, with no filter and no shipped timeout | sharp edge | A shop sharing a database with a legacy application pays for a few thousand tables at every pod start and needs schema read rights across all of them |
| 23 | No exported in-memory `Catalog`, and it cannot live in `crudtest` — `crud/crudtest` is TIER0 and `crud/catalog` is outside the contract manifest | sharp edge | Nothing here can be unit-tested by a consumer without a live database, and the crudtest sweep has already handed the case here |
| 24 | The catalog snippet does not compile in **either** usage guide: `ent.md:1336/:1340` redeclares `src`, and `gorm.md:1255/:1259` does the same and names `db`, which that guide does not define | sharp edge | It is the snippet a consumer copies for the one wiring blocker 2 tells them they need |

Cited, not counted: no runnable example wires `WithFaults`, `catalog.Load` or the
probe — the adapters sweep carries it at `Adapters.md:165`.

## Contested

- **"`faults.Enrich` alone names no field" survives, re-scoped.** Two reviewers
  showed that `postgresSource` fills `Columns` from `ColumnName`
  (`errs/sqlerr/postgres.go:61-63`), so the one-liner does name the field for a
  `NOT NULL` violation on PostgreSQL. They are right and blocker 2 now says so.
  The blocker stays because the sentence in the docs is unqualified and the
  violation the module is sold on is the one it is false for.
- **Blocker 16 (`crudpgx.WithFaults` drops the extractor) is kept here rather
  than handed to the adapters sweep.** A reviewer said it belongs there.
  `Adapters.md` mentions the typed extractor once, as evidence that the pgx
  adapter's default is good (`:97`), and nowhere records that `WithFaults`
  removes it. Handing it over would drop it. The *accessor* half is shared work
  and is credited to `Adapters.md:357-361`.
- **`sqlfault` is not where a constraint→code map goes, and the case for one is
  the `errs` sweep's.** The numbered case round 1 carried for it is cut: it is
  H-ERRS-10 (`docs/ai/usecases/modules/errs/Errs.md:512-553`), same actor, same
  `users_email_key` → `email_taken`, and `errs.CodeMapper` is already declared
  and wired to nothing (`errs/spi.go:27-31`). The reasoning that is local stays
  here because the next reader will otherwise re-propose the map: `fill` runs
  *after* both the code lookup and the kind lookup
  (`crud/sqlfault/classify.go:79-86`), so an override there either lands before
  `KindOf` and needs the new word already in `errs.Codes`, or lands after and
  silently keeps `unique`'s kind — losing the property that the new word decides
  the status.
- **The message-ladder case is cut to a signposting line.** Round 1 carried it
  as a ✅ happy case; both reviewers said it resolves entirely into `errs`
  (`errs/message.go:19-25`, `:77-110`, and `Violation.Params`). It does. The
  residue that is this subsystem's — `docs/modules/*/faults.md` never mentions
  that the sentence a user reads comes from `errs` — is a documentation action
  item and appears as a DX row.
- **Refuse loudly versus degrade quietly is one decision, not two findings.**
  H-FAULTS-16 rates the declaration-time panics the best thing in the subsystem;
  blocker 15 calls one of the same panics an outage. Both are kept, and the
  tension is named in both places. The middle they can share: the panic stays for
  a wiring error — an unknown table, a key that does not identify a row — and a
  constraint that has *gone away* becomes a start-up log plus a dropped term,
  because that is the direction a migration produces.
- **The scope narrowing is contested against the use-case index.**
  `docs/ai/usecases/Index.md:172-176` records "the scope narrows the probe's
  *unique* terms only… `Skip` is the control there" as phase 7's settled outcome.
  This file rates it a serious blocker anyway, on the grounds that a control which
  removes the check is not a narrowing of it. Whoever settles it must edit one of
  the two documents; they cannot both stand.

## Edge cases

### E-FAULTS-01 — The source override points at the other database
**Shape:** seam
**Setup:** A repository over the primary database sits below an opaque decorator, so its author supplies faults.WithSource manually and accidentally passes the analytics source, whose users table has the same shape.
**What the consumer does:** They load the catalog from the primary, wire probe.Full with it, and use the secondary source only to make the declaration pass.
**What must happen:** Declaration must refuse a source/catalog/repository combination it cannot prove belongs to the same physical handle, or the probe must decline rather than turn data from another database into an answer about this write.
**Today:** ❌ wrong or unhandled
**Evidence:** faults.WithSource stores any crud.Source without an identity check (crud/decorators/faults/probe.go:50-57). Declaration binds Full only against its catalog (crud/decorators/faults/probe.go:101-115; crud/probe/declare.go:42-59), then enrichProbed assigns that supplied source to Request.Source (crud/decorators/faults/probe.go:161-166) and Full queries it when there is no matching transaction executor (crud/probe/full.go:119-126). Catalog exposes no handle identity to compare (crud/catalog/catalog.go:13-20). The one explicit-source test proves only that passing the same stub permits an opaque chain (crud/decorators/faults/probe_test.go:328-336); no test uses distinct sources.
**Blast radius:** data leak

### E-FAULTS-02 — CodeOnly still names the field the driver supplied
**Shape:** seam
**Setup:** A public endpoint uses probe.CodeOnly because revealing even the request field is too much for this deployment, and PostgreSQL reports a column on the driver violation.
**What the consumer does:** They turn on CodeOnly together with Full and expect every violation in the returned fault to retain a code but no path.
**What must happen:** The final fault must contain no path from either the probe or the driver; a privacy option cannot depend on which layer first knew the column.
**Today:** ❌ wrong or unhandled
**Evidence:** CodeOnly promises that it applies to the path the probe would fill on the driver's violation (crud/probe/options.go:82-85), but Full only avoids copying a probe path in fold (crud/probe/full.go:223-226). The original driver Source survives merge (crud/probe/full.go:157-160), and the decorator then resolves every path-less violation after the probe returns (crud/decorators/faults/faults.go:118-120; :138-151). TestCodeOnlyModeDropsThePathAndKeepsTheCode tests Full directly with a driver fault carrying no source columns (crud/probe/full_test.go:469-481); no decorator-level CodeOnly test covers a driver-named column.
**Blast radius:** data leak

### E-FAULTS-03 — One Skip disables two different constraints with the same name
**Shape:** degenerate declaration
**Setup:** A MySQL or MariaDB table has a unique key and a foreign key both named k, which their separate namespaces allow.
**What the consumer does:** To stop the unique-key oracle, they call probe.Skip("k") and expect only that unique check to disappear.
**What must happen:** The declaration must either reject an ambiguous name or let the consumer identify the one constraint to suppress. It must never silently remove a second, semantically different check.
**Today:** ❌ wrong or unhandled
**Evidence:** The catalog deliberately preserves distinct constraint families under the same name (crud/catalog/load.go:150-173; :210-218), but Declare accepts a Skip as soon as Table.Constraint finds either one (crud/probe/declare.go:51-54; crud/catalog/catalog.go:128-140). Planning then keys the exclusion on candidate.name alone (crud/probe/plan.go:191-193), so both candidates named k are skipped. TestASkipNamingNoConstraintRefusesToStart covers a missing name and one ordinary name only (crud/probe/declare_test.go:64-72); no collision test exists.
**Blast radius:** silent wrong answer

### E-FAULTS-04 — Two binary tokens in one batch are not recognised as duplicates
**Shape:** boundary
**Setup:** An import writes two rows with the same []byte value into a unique bytea or BINARY token column.
**What the consumer does:** They enable Full for SaveAll because the module says intra-payload duplicates report both row indexes without another statement.
**What must happen:** The no-statement supplement either identifies both equal binary values or marks this part of the batch answer incomplete. It cannot present the promised all-row result as complete after declining the only check that can compare two uncommitted rows.
**Today:** 🟡 partial
**Evidence:** keyOf rejects every non-comparable value before rendering it (crud/probe/dup.go:81-102), so []byte cannot enter the duplicate map even when its bytes are identical. The subsequent SQL terms ask the stored table, not another row in the submitted batch (crud/probe/sql.go:91-115), and Partial is set only by a cap or a probe error (crud/probe/full.go:64-80). TestANonComparableKeyPartMakesARowUnkeyable deliberately pins the omission using []string values (crud/probe/dup_test.go:153-175); there is no []byte duplicate test.
**Blast radius:** silent wrong answer

### E-FAULTS-05 — A zero cap means “use fifty”, without telling the author
**Shape:** boundary
**Setup:** An operator sets WithMaxRows(0), WithMaxConstraints(-1), WithTimeout(0), or WithMaxSavepoints(0) from an environment-derived configuration, intending to disable that work or noticing a bad value.
**What the consumer does:** They deploy expecting the explicit invalid setting to refuse at declaration, or at minimum to be reported as the documented default.
**What must happen:** An invalid explicit limit must fail loudly before traffic, because it changes whether an error response will issue a statement, wait for one, or claim a savepoint.
**Today:** 🟡 partial
**Evidence:** All four option constructors silently leave the defaults in place when their input is not positive (crud/probe/options.go:104-140). Only WithMaxConstraints documents this fallback (crud/probe/options.go:104-106); WithMaxRows, WithTimeout and WithMaxSavepoints do not. The option tests cover positive replacements and timeout behaviour (crud/probe/full_test.go:353-435), with no non-positive setting case.
**Blast radius:** confusing error

### E-FAULTS-06 — A missing catalog panics while binding
**Shape:** misuse
**Setup:** A startup branch loses a catalog value after handling its Load error incorrectly and still constructs probe.Full with a nil catalog interface.
**What the consumer does:** They bind the resource and expect the declaration-time failure style this subsystem advertises: a precise refusal naming the bad wiring.
**What must happen:** The process must fail at declaration with an actionable error such as “probe catalog is nil”, not a nil-interface panic whose relation to the resource is lost.
**Today:** ❌ wrong or unhandled
**Evidence:** Full accepts and stores its Catalog argument without validation (crud/probe/full.go:18-23), then Declare immediately calls f.cat.Table (crud/probe/declare.go:38-45). No guard covers a nil catalog. Declaration tests cover an empty fake catalog but not nil (crud/probe/declare_test.go:13-27), and the binding test only asserts that a known-table failure panics (crud/decorators/faults/probe_test.go:338-346).
**Blast radius:** crash

### E-FAULTS-07 — A conditional handler crashes on the first conflict
**Shape:** misuse
**Setup:** An application builds its options conditionally, leaves a probe.Handler nil for one environment, and still passes it to faults.WithProbe.
**What the consumer does:** They start normally, then receive their first classified database refusal.
**What must happen:** Bind must reject a nil handler with the operation that is unwired. A release cannot turn a routine 409 into a request-time panic because an optional feature was absent.
**Today:** ❌ wrong or unhandled
**Evidence:** WithProbe stores the handler unchanged (crud/decorators/faults/probe.go:28-39); declare records a nil handler without a Declarer or Savepointer check firing (crud/decorators/faults/probe.go:101-115). The first fault then calls pc.h.Enrich unconditionally (crud/decorators/faults/probe.go:161-177). No faults probe test passes a nil handler.
**Blast radius:** crash

### E-FAULTS-08 — A conditional sqlfault component crashes only on a refusal
**Shape:** misuse
**Setup:** A service conditionally supplies a custom vocabulary or extractor, but the selected *errs.Codes is nil or the selected ExtractorFunc is nil.
**What the consumer does:** They construct sqlfault.New successfully, then a real database constraint is hit hours later.
**What must happen:** The constructor must refuse an unusable component, or classification must degrade safely just as Wrap does for a nil classifier.
**Today:** ❌ wrong or unhandled
**Evidence:** WithCodes stores a nil vocabulary unchanged (crud/sqlfault/classify.go:26-29), and Classify dereferences it at c.codes.KindOf (crud/sqlfault/classify.go:79-85). WithExtractor likewise stores its input unchanged (crud/sqlfault/classify.go:31-33); a non-nil ExtractorFunc interface holding a nil function reaches f(err) (crud/sqlfault/extract.go:19-22) through c.extract (crud/sqlfault/classify.go:120-125). New deliberately guards only nil Option values (crud/sqlfault/classify.go:44-51). No test supplies a nil Codes or nil ExtractorFunc.
**Blast radius:** crash

### E-FAULTS-09 — One Full value serves twenty models without cross-wiring their table plans
**Shape:** concurrency
**Setup:** An application creates one expensive probe.Full value over its catalog and reuses it while binding many models, potentially at the same time.
**What the consumer does:** They avoid rebuilding the configuration and expect each repository to keep the candidate constraints for its own table.
**What must happen:** Binding a later model must not move an earlier repository onto the later table or share mutable declaration state.
**Today:** 🟡 partial
**Evidence:** Full.Declare copies the handler before attaching meta, table and candidates (crud/probe/declare.go:35-59), so the source Full remains unbound. TestDeclaringTwoModelsFromOneFullValueGivesTwoHandlers asserts the first remains on docs and the second binds orgs (crud/probe/declare_test.go:90-111). It is a sequential declaration test; no test pins concurrent reuse, so the source shape is encouraging rather than a release-proof guarantee.
**Blast radius:** none

### E-FAULTS-10 — A failed enrichment probe must not replace the write refusal
**Shape:** partial failure | seam
**Setup:** A write has already returned a classified unique conflict, then the probe query blocks, its context is cancelled, or its connection fails.
**What the consumer does:** It returns the write failure to the client and treats enrichment as extra detail, not as permission to change a 409 into a timeout or 500.
**What must happen:** At most the configured probe work is attempted; a timeout or probe failure preserves the original classified fault and marks it partial. The response must not retry or extend the follow-up work after that failure.
**Today:** 🟡 partial
**Evidence:** `Full.run` gives its one follow-up query a derived timeout and returns its error (`crud/probe/full.go:98-129`); `Full.Enrich` preserves the driver fault and marks it partial on that error (`:67-80`). `enrichProbed` keeps the original fault, reports the probe error only to `WithProbeError`, and finishes it as partial (`crud/decorators/faults/probe.go:157-177`; `faults.go:102-130`). `TestAProbeThatErrorsKeepsTheDriversViolationAndSaysItIsPartial` and `TestAProbeFailureIsHandedToTheCallerAndNotToTheClient` pin the error control (`crud/probe/full_test.go:70-100`; `crud/decorators/faults/probe_test.go:452-465`); the direct timeout control is `crud/probe/full_test.go:412-435`. No decorator-level cancelled-context or one-follow-up-count control was found.
**Blast radius:** confusing error

## Edge verdict

The worst edge is the explicit source override: it is the documented escape hatch for an opaque chain, yet it can turn another database's state into this resource's response with no declaration-time check. Privacy and disclosure controls are also not closed: CodeOnly is undone after the probe, and a name-only Skip can silently suppress two separate checks. `Full` preserves a classified write refusal when its one follow-up fails, but the decorator has no direct cancelled-context control for that contract. Conditional configuration at the error boundary still has several request-time panics. These are not exotic driver failures; they are the ordinary nils, duplicate names, binary values and failed follow-up work a service reaches while wiring a framework.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | faults.WithSource can direct a probe planned from one catalog to a different physical database, with no identity check | blocker | A secondary database can decide whether this endpoint says a value exists. That is both a cross-database silent wrong answer and an existence oracle leak. |
| 2 | CodeOnly does not prevent the final faults decorator from resolving a driver-named column into a public path | blocker | A deployment selecting the explicit “do not name the field” control still exposes it for the driver paths most likely to carry one. |
| 3 | Skip is keyed solely by constraint name although the catalog deliberately preserves same-name key and foreign-key families | blocker | Opting out of one oracle check silently turns off another violation class and still renders the result complete. |
| 4 | Intra-payload duplicate detection drops equal non-comparable binary values without Partial | serious | A batch caller is promised both bad row indexes; this ordinary database value type gets neither the Go-side second row nor an incompleteness marker. |
| 5 | Nil catalog, handler, vocabulary or extractor values are accepted and panic at Bind or on the first classified refusal | serious | A conditional production configuration turns a normal 409 into either a rollout crash or a request-time panic instead of an actionable declaration error. |
