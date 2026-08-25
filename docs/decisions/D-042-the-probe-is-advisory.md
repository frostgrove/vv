# D-042 — The probe is advisory; the index is the truth

**Status:** accepted — in force from phase 7 (`ROADMAP-errors.md` §14)
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
response reveals that a value exists. `docs/usecases/` gap 3 records this for
plain 409s; the probe multiplies it. The controls — value never echoed by
default, per-constraint opt-out, scope-aware probing from the `security.Policy`,
code-only mode — make the trade adjustable, not absent. A unique constraint a
client can trigger is an oracle by construction.

## What it forbids

- Do not let a clean probe suppress the driver's violation.
- Do not let a probe error change the status. Keep the classification, set
  `Partial`.
- Do not return a capped set without `Partial: true`.
- Do not probe a foreign key without the every-column-non-null guard.
- Do not bind an explicit JSON null as `= NULL`. [[D-002]]'s third state reaches
  the planner as nil, and `WHERE email = NULL` is never true, so the probe would
  report clean for a column about to fail NOT NULL.
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

## Where it lives

Nothing yet. `probe/TODO.md` holds the place; phase 7 creates it.

- `crud/executor.go:ExecutorFor` — the probe resolves its executor through it,
  which is what makes "never probe on another connection" enforceable ([[D-009]]).
- `crud/dialect.go:Dialect.Upsert` — where the skip set comes from.
- `repo/basic/repository.go` — `UpdatePlan`, and the loaded row the values come
  from ([[D-010]]).

## Proven by (owed)

Phase 1 shipped the marker: `TestTheStepsThatWriteNothingElseReadsStillWrite` in
`errs/build_test.go` pins `Builder.Partial` onto the fault and onto the wire as
`"partial":true`, with the uncapped fault — no `partial` key at all — as its
control. Nothing sets it yet, so the evidence this decision rests on is still
owed.

Phase 7 owes both halves, and the second is the one that catches the NULL bug:

- probe off ⇒ one violation; probe on ⇒ **three distinct codes at three distinct
  paths**. Counting to three passes for one violation repeated.
- the negative twin: a payload with exactly one real violation yields **exactly
  one**. This is what catches an unguarded NULL foreign key, a partial index
  replayed wrongly, or a prefix index.
- past the cap the answer carries `Partial: true`; a probe that errors keeps the
  driver's violation instead of becoming a 500.
- with the value-echo mode on the value appears; with the default it does not.
- at least eight violations spanning names, indices and equal-prefix paths, built
  in reverse, render byte-identically.
- inside a transaction without savepoints `Full` degrades to one violation rather
  than erroring; **with** `WithSavepoints()` it does not degrade; and a foreign
  transaction is never given a savepoint.

## Open questions this decision defers

`ROADMAP-errors.md` §16 owns them and each needs answering before phase 7:
the cap defaults (a cap without a number is not a cap), composite primary keys
(the exclude-my-own-row clause assumes a single-column key), and whether
pre-flight is ever a default for a named endpoint shape.

One of them is answered. Whether `crud/crudtest`'s recorder grows a
`DataSource()` was listed here because it read as the difference between the
probe having a unit-test seam and being integration-only. Phase 6 settled it:
**no**, and the seam exists anyway, because `crud.KeyOf` keys an unidentifiable
source as itself. [[D-041]] has the argument and the test that pins it.

## See also

[[D-041]] [[D-008]] [[D-009]] [[D-010]] [[D-011]] [[D-014]] [[D-019]] [[D-032]] [[UC-004]] [[UC-017]]
