# D-042 — The probe is advisory; the index is the truth

**Status:** accepted — in force since phase 7
**Invariant:** The probe may only narrow the truth, never widen it. A probe that finds nothing never suppresses the driver's own violation, a probe that errors keeps it, and a probe that hits a cap says so rather than presenting a partial answer as complete.

## The decision

A database reports one violation per failed statement. The probe issues one
extra query to find the others. Everything about it is subordinate to the
statement that actually failed:

- the driver's violation is always in the answer, whatever the probe finds;
- a probe error keeps that violation, sets `Partial: true` and logs;
- a cap hit sets `Partial: true` and the envelope says the set is incomplete;
- the probe never replaces the constraint. It is advice about the same index.

## Why

**Because a probe that invents violations is worse than the single-violation
status quo it replaces.** This is not hypothetical — it was measured. A nullable
foreign key left NULL satisfies its constraint (SQL's MATCH SIMPLE), so a bare
`NOT EXISTS(SELECT 1 FROM orgs WHERE id = NULL)` evaluates to `true` and the
probe reports `foreign_key` on a field that is correct. Against PostgreSQL 17:
the probe says violation, the insert succeeds. Composite foreign keys are worse
— *any* NULL column disables the check entirely, so the guard is "every column
non-null", not "this one".

A false 422 on a correct field is a bug the client cannot work around: there is
nothing to fix.

**Because a probe fails more often than the write it explains.** It re-binds
values from a statement that already failed, so a write rejected for a bad type
re-binds the same value and fails the same way. If a probe failure downgraded
the answer, the most failure-prone part of the design would turn a correct 409
into an opaque 500 — the exact inversion of the point. §2's "internal first"
precedence governs a failure of the *classification*, not of enrichment.

**Because a partial answer presented as complete is worse than one violation.**
A client shown four violations reasonably concludes there are four. Caps are
necessary — a hostile client posting a 10 000-row batch must not turn one failed
write into a 10 000-way probe, and a table with forty unique indexes must not
either — so the truthfulness has to be explicit rather than implied.

**Because the database's answer is the only one that is right.** MySQL makes the
argument: under `STRICT_TRANS_TABLES` a too-long value is an error, and without
it the same value is a warning and a silent truncation. A Go-side length check
would report a violation the server would never raise, on a deployment the
library cannot see. So NOT NULL, length, range and enum membership are not
checked in Go. The one Go-side check that is unambiguously correct is
intra-payload duplicates in a batch, because that is a fact about the payload
rather than about the database.

**Because the skip set is per dialect and not per rule.** `Save` *is* the upsert
path ([[D-011]]), and an upsert swallows the conflicts its own target covers.
PostgreSQL emits `ON CONFLICT (pk) DO UPDATE`, which swallows the primary key
only; MySQL emits `ON DUPLICATE KEY UPDATE`, which swallows *every* unique key.
The probe derives its skip set from `crud.Dialect`'s upsert form, never from a
hard-coded rule — and the resulting difference is observable, so [[D-019]] names
it.

**Because the oracle is real and is not closed by this decision.** The probe
queries rows the caller may not be allowed to see, and a unique-violation
response reveals that a value exists. `docs/ai/usecases/` gap 3 records this for
plain 409s; the probe multiplies it. The controls — value never echoed by
default, per-constraint opt-out, scope-aware probing from the `security.Policy`,
code-only mode — make the trade adjustable, not absent. A unique constraint a
client can trigger is an oracle by construction.

## The caps, as numbers

A cap without a number is not a cap. `ROADMAP-errors.md` §16 left choosing them
here; phase 7 chose, and the numbers are exported so a reader can see them from
the API rather than from a comment.

| cap | default | why this number |
|---|---|---|
| constraints per request | 16 | one term per constraint, and relevance already narrows by written columns. §8's own hostile case is a table with forty unique indexes; sixteen *relevant* constraints on one write is already pathological |
| rows per batch | 50 | a batch probe is one flat statement of one term per constraint per row, so 50 × 16 is 800 columns of one result row — inside PostgreSQL's 1664-column limit with room to spare, and it bounds §8's 10 000-row hostile batch to a fixed cost |
| probe time | 250ms | around the probe statement only. The write has already failed and the client is waiting; a quarter second covers sixteen indexed `EXISTS` on a warm database and cannot double a request on a cold one. A timeout takes the probe-error path |
| savepoints per transaction | 32 | half of PostgreSQL's 64-entry subxid cliff, counted per transaction rather than per repository. The overflow is not a round trip — it forces pg_subtrans lookups on every reader in the cluster |
| catalog load time | **no cap** | it runs once at declaration on a context the application owns. A default timeout turns a slow but healthy start-up into the fatal refusal [[D-041]] makes it, which is worse than a hang with an obvious symptom |

Every one of them is overridable, and hitting any of them sets `Partial`.

The last row settles something phase 6 left provisional: `crud/catalog/reload.go`'s
intervals — 1s doubling to 5 minutes, with a 1s floor between passes — are
decided rather than waiting on this decision.

## Composite primary keys

`ROADMAP-errors.md` §16 asked whether the probe forces the decision, because the
exclude-my-own-row clause assumes a single-column key. **It does not force it,
and it refuses rather than being silently wrong.**

`crud.Schema` has one `PK` field, so a composite primary key is not declarable
today and the clause is exactly as general as the repository already is. The
reachable harm is a different one: a model with a single `pk` field mapped onto a
table whose *real* primary key is composite. There the repository's own
`UPDATE … WHERE pk = ?` is already wrong — it touches every row sharing that half
of the key — and the probe would silently compound it.

So a declaration refuses unless the catalog confirms the model's key column is a
row identity **on its own**: the table's primary key when that is single-column,
or a unique constraint or index over exactly that one column. This never fires
for a correctly declared model, it turns a silent trap into a start-up error, and
it leaves general composite-key support to the seam that would have to carry it.

## Pre-flight is never a default

§16 asked whether pre-flight probing should ever be the default for a named
endpoint shape. **No, for any endpoint shape**, and phase 7 ships no pre-flight
mechanism at all.

Three reasons. It costs a query on every happy-path request, including the ones
that were going to succeed. The TOCTOU window between the check and the insert
makes a clean answer a lie under concurrency, so the index stays the truth
either way. And the framework cannot know an endpoint is a signup form — that is
application knowledge, and [[D-037]] already refuses to resolve a component by
what it looks like. Shipping the mechanism untested would be worse than not
shipping it, so this records the settled default for whichever later phase builds
it.

## The merge rule

The probe gives a MySQL duplicate key a `field` for the first time, and it does
so without ever removing the driver's own violation.

Where the driver named a constraint — PostgreSQL — its violation is matched to
the probe's by that name. Where it named none — MySQL, MariaDB and SQLite carry
no constraint in their structured error at all ([[D-019]] difference 10) — it is
matched by code, and **only when exactly one** probe violation carries that code.
With two there is no way to tell which of them the engine stopped at, and folding
into the wrong one would move a path onto a field that is correct.

The fold goes one way round only: the driver's violation is what stays and gains
the path it never had; the probe's duplicate is what goes. That is an addition to
this decision rather than a weakening of it — nothing removes a violation the
probe did not itself find.

## What it forbids

- Do not let a clean probe suppress the driver's violation.
- Do not let a probe error change the status. Keep the classification, set
  `Partial`.
- Do not return a capped set without `Partial: true`.
- Do not probe a foreign key without the every-column-non-null guard.
- Do not bind an explicit JSON null as `= NULL`. **Corrected by phase 7**, and
  the correction is a narrowing: a key part that is NULL takes the whole
  constraint out of the plan rather than binding `IS NULL`. PostgreSQL 15+ has
  `UNIQUE … NULLS NOT DISTINCT` and the catalog does not record which semantics a
  key has, so binding `IS NULL` reports a violation under the default
  `NULLS DISTINCT` — a false positive on a correct field. Skipping loses a
  violation only under `NULLS NOT DISTINCT`. §8's rule survives aimed at the NOT
  NULL check this package does not ship: nothing here reports clean *instead of*
  a NOT NULL failure, because nothing here checks NOT NULL at all.
- Do not take probe values from the change set. [[D-010]] drops any field whose
  value already matches the stored one, so `UNIQUE (tenant_id, slug)` with only
  `slug` changing has no `tenant_id` in the change set. The change set decides
  **which** constraints matter; the merged row supplies **what** is bound.
- Do not read the result set by alias. PostgreSQL truncates identifiers at 63
  bytes with a `NOTICE` no driver surfaces, so two constraints sharing 61
  characters collide silently and the row mis-attributes. Read by position;
  catalog order carries the identity ([[D-014]]).
- Do not emit violations in map order. The total order is
  `Kind precedence → Path → Code → constraint name`, which is [[D-014]]'s
  argument one layer up and the only thing that makes a body testable.
- Do not implement a constraint in Go. Two implementations disagree eventually
  and the database's is the one that is right.
- Do not make pre-flight probing the default. It costs a query on the happy path
  and opens a TOCTOU window.
- Do not confirm a row the caller cannot read. [[D-008]] keeps 404 ahead of 403
  and the probe is not exempt.
- Do not evaluate a CHECK constraint. The catalog carries one only as the
  engine's own text, whose shape differs per engine, and recovering the
  expression from it is the DDL parsing [[D-041]] forbids. Evaluating one against
  a synthesised candidate row also needs defaults and generated-column
  expressions the catalog does not hold, and every gap there widens.
- Do not replay a constraint the catalog flagged as unreproducible — partial,
  prefix, expression-keyed or deferrable. Each of them makes the probe claim a
  check it did not perform.
- Do not correlate a keyed `Save` to a stored row. It writes every insertable
  column and there may be no stored row at all, so an upsert that inserts a fresh
  row would probe nothing.
- Do not report a column written with the value it already holds. A unique term
  excludes the row the write aims at, and a restrict term fires only on a real
  change.
- Do not take a savepoint inside a transaction vv did not open, whatever the
  savepoint mode says.

## Where it lives

- `crud/probe/doc.go` — the rules no signature carries, including the four named
  non-deliveries and the cap numbers.
- `crud/probe/probe.go` — `Handler`, `Declarer`, `Savepointer`, `Request`, `Row`,
  `Simple`.
- `crud/probe/full.go` — `Full`, `Enrich`, `runs`, `run`, `merge`, `same`, `fold`,
  `violation`, `path`, `truthy`.
- `crud/probe/plan.go` — `candidatesFor`, `reproducible`, `restricting`, `planFor`,
  `swallowed`, `bind` — the skip set, and every narrowing this decision asks for.
- `crud/probe/sql.go` — the three term shapes and the statement they go into.
- `crud/probe/dup.go` — the one Go-side check, and [[D-025]]'s collapse written down
  rather than repeated.
- `crud/probe/options.go` — the caps as exported constants, and the four oracle
  controls.
- `crud/probe/declare.go` — the declaration-time refusals, including [[D-041]]'s owed
  one and the composite-key answer above.
- `crud/decorators/faults/probe.go` — the wiring: the per-verb defaults, the
  savepoint wrap around the write, and the probe error's only way out.
- `crud/executor.go:OwnedExecutorFor` / `:ClaimSavepoint` / `:Sourced` — the seam
  change the transaction matrix needed. The probe resolves its executor through
  `crud.ExecutorFor`, which is what makes "never probe on another connection"
  enforceable ([[D-009]]).
- `crud/dialect.go:UpsertScope` — where the skip set comes from, replacing
  `Dialect.Upsert` as the thing to read: the clause's *text* does not say what it
  swallows.
- `crud/dialect.go:StatementRollback` — which side of the transaction matrix a
  dialect is on, without a name check ([[D-019]]).
- `crud/update.go:DefinedChanges` — the values, which `DefinedFields` does not
  carry.
- `crud/catalog/catalog.go:Referrers` — the inbound direction, which no lookup on
  `Catalog` can express and which `restrict` needs.
- `crud/sqlrepo/repository.go:Source` — three lines, and the whole of
  `crud.Sourced`.

## Proven by

Phase 1 shipped the marker: `TestTheStepsThatWriteNothingElseReadsStillWrite` in
`errs/build_test.go` pins `Builder.Partial` onto the fault and onto the wire as
`"partial":true`, with the uncapped fault — no `partial` key at all — as its
control. Phase 7 is what sets it.

Both halves, and the second is the one that catches the NULL bug:

- `TestOneFailedWriteBecomesEveryViolationItCaused` in
  `test/integration/probe_test.go` — probe off ⇒ one violation, probe on ⇒
  **three distinct codes at three distinct paths**, on five targets with a
  counter. The assertion is on the *set* of `(code, path)` pairs, because
  counting to three passes for one violation repeated.
- `TestAPayloadWithOneRealViolationYieldsExactlyOne` — the negative twin, live on
  four engines. One real violation beside a NULL nullable foreign key, a
  composite foreign key with one NULL column, a NULL half of a composite unique
  key, and the unreproducible key's bait. Its control is
  `TestTheSamePayloadWithARealMissingParentYieldsTwo`: a probe that closed the
  NULL hole by dropping foreign keys altogether fails there.
- `TestAnUpdateDoesNotReportARowCollidingWithItself` — the own-row exclusion and
  the restrict change-guard, on one payload.
- `TestTheUnreproducibleKeyIsNeverProbedAndItsPlainTwinIs` — partial on
  PostgreSQL and SQLite, a prefix key on MySQL and MariaDB, with the reproducible
  twin over the same shape as the control.
- `TestPastTheCapTheAnswerSaysItIsIncomplete`, with the cap raised as its
  control, and `TestAProbeThatErrorsKeepsTheConflict`, whose two halves are both
  409 and neither 500.
- `TestTheOffendingValueReachesTheBodyOnlyWhenAsked` — the same write rendered
  twice through a template naming `{value}`.
- `TestTheTransactionMatrix` — twenty arms with a counter: outside a transaction,
  inside one vv opened with and without savepoints, and inside a foreign one.
- `TestTheSameFailingRequestTwiceProducesTheSameBody` — byte-identical over five
  renders, with a count of violations in the body as the control that the
  comparison measures an order.
- `TestTheViolationOrderIsTotalAndByteIdentical` in `port/violations_test.go` —
  eight violations spanning names, indices and equal-prefix paths, built in
  reverse. Phase 4 wrote it beside the renderer and phase 9 moved it down with
  the pipeline ([[D-045]]); phase 7 is what produces eight violations for it to
  order.
- The unit half, in `crud/probe/full_test.go`, `crud/probe/dup_test.go`,
  `crud/probe/declare_test.go` and `crud/decorators/faults/probe_test.go`: every skip
  with its unskipped twin, the merge rule with both its controls, the caps, the
  timeout, the savepoint budget, and the foreign transaction that is never given
  a savepoint.
- `crud/executor_test.go:TestATransactionVVOpenedIsMarkedOwnedAndAForeignOneIsNot`
  and `:TestASavepointClaimCountsPerTransactionAndNotPerRepository` — the seam
  change this decision rests on.
- `crud/dialect_test.go:TestOnlyADialectThatSaysSoSwallowsThePrimaryKeyOnly` —
  with a dialect implementing neither optional interface as the control that the
  defaults are the narrowing ones.

## Open questions this decision no longer defers

All three are answered above and struck from `ROADMAP-errors.md` §16: the cap
defaults, composite primary keys, and whether pre-flight is ever a default.

A fourth was answered earlier. Whether `crud/crudtest`'s recorder grows a
`DataSource()` was listed here because it read as the difference between the
probe having a unit-test seam and being integration-only. Phase 6 settled it:
**no**, and the seam exists anyway, because `crud.KeyOf` keys an unidentifiable
source as itself. [[D-041]] has the argument and the test that pins it. Phase 7
spent that seam: every unit test in `crud/probe/` drives a recorder.

## See also

[[D-041]] [[D-008]] [[D-009]] [[D-010]] [[D-011]] [[D-014]] [[D-019]] [[D-025]]
[[D-032]] [[D-037]] [[D-043]] [[UC-004]] [[UC-017]] [[FL-017]] [[FL-014]] [[FL-009]]
