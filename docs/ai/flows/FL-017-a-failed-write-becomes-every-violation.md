# FL-017 — A failed write becomes every violation it caused

**Entry point:** `crud/decorators/faults/probe.go:enricher.probed`
**Implements:** [[UC-017]] [[UC-004]] · **Governed by:** [[D-042]] [[D-041]] [[D-019]] [[D-009]] [[D-010]] [[D-011]] [[D-014]] [[D-032]] [[D-043]] [[D-044]] [[D-008]] [[D-021]] [[D-025]]

[[FL-014]] carries a refused statement as far as a fault with **one** violation
in it, because that is what a database reports: the first constraint it reaches
ends the statement. This flow is the second one, which finds the rest.

Everything here is subordinate to the statement that actually failed. The
driver's violation is in the answer whatever the probe does, a probe that fails
keeps it and marks the answer incomplete, and nothing is ever invented
([[D-042]]).

## The path

1. **Declaration.** `sqlrepo.Blueprint.Bind` builds the middleware chain.
   `crud/decorators/faults/probe.go:enricher.declare` runs then:

   - the datasource comes from `crud.SourceOf(next)`, which asks each layer and
     follows `crud.Nexter` down until one answers — ending at
     `crud/sqlrepo/repository.go:repository.Source`. A `crud.Core` embedded in a
     decorator promotes only the interface's own methods, so a bare assertion on
     the layer directly below made the order decorators were listed in decide
     whether the probe worked: a `security.Gate` between `faults.Enrich` and the
     repository answered no ([[D-061]]). The walk ends, and the declaration
     refuses, only where a decorator implements neither `Sourced` nor `Next` —
     `faults.WithSource` is the way out.
   - each wired handler is bound with `probe.Declarer.Declare`
     (`crud/probe/declare.go`), which refuses at start-up rather than at request time
     ([[D-021]]): a table the catalog does not know (`probe.ErrUnknownTable`), a
     primary-key column that is not a row identity on its own
     (`probe.ErrKeyDoesNotIdentify`), an opt-out naming no constraint
     (`probe.ErrUnknownConstraint`).
   - `crud/probe/plan.go:candidatesFor` reads the catalog once and keeps every
     constraint the probe can replay from a value.

2. **A write runs, wrapped.** `enricher.Save` / `.SaveAll` / `.Update` build a
   `probe.Request` — `insertRequest` and `updateRequest` in
   `crud/decorators/faults/probe.go` — and hand the write to
   `enricher.probed`. Under `probe.WithSavepoints()` on a transaction vv owns,
   `enricher.savepoint` takes one **before** the write through
   `crud.BeginnerOf(ex).Begin`, because a savepoint cannot be taken after the
   fact. `BeginnerOf` and not an assertion: a `Source` wrapped for
   instrumentation is still a `Beginner` underneath, and losing it here means
   `spRefused` and a probe that quietly declines ([[D-061]]).

3. **The write fails.** `enricher.enrichProbed` finds the fault
   (`errs.AsFault`) and returns anything that is not one untouched — this
   decorator never invents a fault. Under a savepoint the transaction is
   restored first (`Tx.Rollback`, which is `ROLLBACK TO SAVEPOINT`), and
   `Request.Recovered` records that it was.

4. **`probe.Handler.Enrich`.** `crud/probe/full.go:full.Enrich`.
   `full.runs` answers the transaction matrix; `full.planFor`
   (`crud/probe/plan.go`) turns the candidates into terms; `full.duplicates`
   (`crud/probe/dup.go`) finds intra-payload duplicates with a map and no statement;
   `full.statement` (`crud/probe/sql.go`) renders one statement through
   `crud.NewSQL`; `full.run` sends it through `crud.ExecutorFor` under
   `context.WithTimeout`, and reads the answer **by position**.

5. **The merge.** `full.merge` and `full.same` fold the driver's own violation
   into the probe's where they provably describe the same failure, so a MySQL
   duplicate key gets a `field` for the first time. The driver's violation is
   what stays; the probe's duplicate is what goes.

6. **The hop.** `full.path` prefixes the row index — the only part of the path
   this package owns — and calls `Request.Resolve`, which is
   `enricher.resolvePath`: the column-to-field translation the faults decorator
   already owned ([[D-043]], one hop, one owner).

7. **The answer.** `enricher.finish` fills the verb and the entity, resolves any
   violation the probe did not, marks the fault `Partial` where a cap or a
   failure cut something out, and sorts with `errs.SortViolations` so the fault
   and the body render in one order.

## The statement

One flat statement, one boolean column per constraint per row:

```sql
SELECT EXISTS(SELECT 1 FROM "pb_doc" AS vvt WHERE vvt."email" = $1
              AND vvt."id" <> $2)                                        AS "c0",
       (NOT EXISTS(SELECT 1 FROM "pb_org" AS vvp WHERE vvp."id" = $3))   AS "c1",
       ((vvcur."code" <> $4)
        AND EXISTS(SELECT 1 FROM "pb_note" AS vvc
                   WHERE vvc."doc_code" = vvcur."code"))                 AS "c2"
  FROM "pb_doc" AS vvcur WHERE vvcur."id" = $5
```

**One flat statement and not a derived table of the batch's rows.** §8 of
`ROADMAP-errors.md` called for a `VALUES`-derived table so each violation could
carry a row index, and phase 7 measured why it cannot be one. A `UNION ALL` of
one-row `SELECT`s does run on all four engines — but PostgreSQL resolves an
untyped parameter inside it to `text`, so `vvt."tenant_id" = vvv."c0"` is
`operator does not exist: bigint = text`, SQLSTATE `42883`. Casting the first arm
fixes PostgreSQL and breaks MySQL, whose `CAST` target vocabulary is not its
column types: `CAST(? AS varchar(64))` is a syntax error there. Binding every
row's values directly needs no type at all, the row index is known by position in
Go, and the caps bound what it costs.

**The `FROM` is only there for an update.** It is what supplies the unchanged
half of a composite key: [[D-010]] drops a column whose value already matches the
stored one, so `UNIQUE (tenant_id, slug)` with only `slug` changing has no
`tenant_id` to bind. `vvcur."tenant_id"` reads it from the row the update did not
change, which is more current than any copy the decorator could have carried.

**Placeholders and quoting go through `crud.Dialect`,** because `$1` is
PostgreSQL's alone and `AS "x"` is a string literal on MySQL without
`ANSI_QUOTES` ([[D-019]]).

**Results are read by position.** PostgreSQL truncates an identifier at 63 bytes
with a `NOTICE` no driver surfaces, so two constraints sharing their first 61
characters would alias onto one column. Catalog order carries the identity
instead ([[D-014]]).

## What is probed, and what is skipped

| | term | skipped when |
|---|---|---|
| `unique` | `EXISTS` over this table, own row excluded | no key column was written; any bound key part is NULL; the upsert's own clause swallows it |
| `foreign_key` | `NOT EXISTS` over the parent | no referencing column was written; **any** referencing column is NULL |
| `restrict` | `EXISTS` over the child, guarded by the value actually changing | the write is not an update; the referenced column was not written, or written NULL; `ON UPDATE` is `CASCADE`, `SET NULL` or `SET DEFAULT` |

And, whatever the kind, `crud/probe/plan.go:reproducible` drops four shapes the
catalog records flags for and nothing here can replay from a value: a **partial**
index, a **prefix** key, an **expression** key part, and a **deferrable**
constraint. Each of them would make the probe claim a check it did not perform.

Nothing else is probed. CHECK constraints, NOT NULL, length, range and enum
membership are named non-deliveries with reasons in `crud/probe/doc.go` and
[[D-042]].

## The transaction matrix

| the write ran… | PostgreSQL | MySQL / MariaDB / SQLite |
|---|---|---|
| outside any transaction | `Full` | `Full` |
| inside a transaction vv opened, or joined | one violation, unless `probe.WithSavepoints()` | `Full` |
| inside a **foreign** transaction | one violation, always | `Full` |

Which side a dialect is on comes from `crud.StatementRollback` and never from its
name. A foreign transaction is never given a savepoint: an ent or gorm
transaction has its own savepoint stack, and `ROLLBACK TO SAVEPOINT` in the
middle of somebody else's unit of work can discard work its owner has not
finished with. Telling the two apart is what `crud.OwnedExecutorFor` is for —
before phase 7 the seam could not answer it, because `crud.InTx` and a foreign
executor binding pushed the same ownership shape. `Session` now supplies the
safe foreign binding without changing that ownership rule ([[FL-009]]).

The probe never issues `SAVEPOINT` itself. `crudsql.Tx.Begin` already does, off a
counter it owns, and a hand-rolled name can collide with one the seam issued.

## Where the decisions bite

- **[[D-042]] — the probe may only narrow.** Every skip above is a narrowing and
  every one of them was chosen over a guess. The measured case is the nullable
  foreign key: against PostgreSQL 17, a bare
  `NOT EXISTS(SELECT 1 FROM orgs WHERE id = NULL)` is `true`, so an unguarded
  probe reports `foreign_key` on a field that is correct while the insert
  succeeds.
- **[[D-011]] and [[D-019]] — the upsert skip set is the dialect's.**
  `crud/probe/plan.go:full.swallowed` asks `crud.UpsertScope`. PostgreSQL and SQLite
  emit `ON CONFLICT (pk) DO UPDATE` and swallow the primary key only; MySQL emits
  `ON DUPLICATE KEY UPDATE` and swallows every unique key, and does not implement
  the interface. "Swallows everything" is the default for a dialect that says
  nothing, which is the narrowing direction.
- **[[D-009]] and [[D-032]] — where the statement runs.** `crud.ExecutorFor`
  over the repository's own source, so a transaction's uncommitted rows are
  visible and a replica never decides a write. A `crud.ReadWrite` pair forwards
  `Query` to its primary.
- **[[D-008]] — the probe does not confirm a hidden row.** `probe.WithScope`
  takes the predicate the `security.Policy` already carries. Writes carry no
  transport scope, so it cannot come from `crud.WithScope`.
- **[[D-043]] — one hop per layer.** The probe owns the row index and nothing
  else; the column-to-field hop is handed in as `Request.Resolve`.
- **[[D-025]] — a non-comparable key.** `crud/probe/dup.go:keyOf` refuses to key a row
  on a value it cannot render faithfully, rather than collapsing it onto a
  per-type constant and reporting the whole batch as duplicates of each other.

## Traps

- **`ExecutorFrom` is the wrong question.** With a foreign transaction scoped to
  another handle it says "in a transaction" while this repository's write runs
  outside one. `crud.OwnedExecutorFor(ctx, src)` is the one to ask.
- **The change set says *which*, the row says *what*.** Reading probe values off
  `UpdatePlan.Changes` loses the unchanged half of a composite key. The decorator
  uses `crud.DefinedChanges`, which is `Writes` and not `Changes`.
- **A keyed `Save` is not an update.** It writes every insertable column and there
  may be no stored row at all, so correlating it to one would make an upsert that
  inserts a fresh row probe nothing. `Request.Stored` is what separates them, and
  it is why an upsert produces no `restrict` terms.
- **A column written with the value it already holds.** `DefinedChanges` reports
  every column the DTO defined, including one whose value did not change. Two
  rules keep that from becoming a violation: a unique term excludes the row the
  write is aiming at, and a restrict term fires only when the value actually
  changes.
- **A savepoint budget counts up and never down.** PostgreSQL's 64-entry subxid
  cache overflows on the number of subtransactions a top-level transaction has
  assigned XIDs to, and releasing a savepoint does not give the entry back. The
  overflow is cluster-wide read amplification, not a round trip.
- **The probe's error is advisory.** It never reaches a client. Wire
  `faults.WithProbeError` or it reaches nobody at all.

## Failure modes

| What goes wrong | Where it is caught | What the caller gets |
|---|---|---|
| the catalog does not know the model's table — including a catalog that read zero rows | `probe.(*full).Declare` | the process refuses to start, naming the table and the dialect |
| the model's key is not a row identity on its own | `probe.(*full).Declare` | the same, naming the column |
| an opt-out names a constraint that was renamed | `probe.(*full).Declare` | the same, naming the constraint |
| the probe statement is refused | `full.run` → `enrichProbed` | the driver's own violation, the same status, `Partial: true`, and the error to `faults.WithProbeError` |
| the probe takes longer than its timeout | `context.WithTimeout` in `full.run` | the same |
| more relevant constraints than the cap allows | `full.planFor` | the terms that fitted, `Partial: true` |
| more rows than the cap allows | `full.planFor` | the rows that fitted, `Partial: true` |
| the savepoint budget is spent | `enricher.savepoint` → `spRefused` | the write runs unwrapped, the probe declines on an engine that poisons, `Partial: true` |
| an update whose row is gone by the time the probe runs | `full.run` reads no rows | the driver's violation alone |
| a catalog that is not a `catalog.Referrers` | `candidatesFor` | no `restrict` terms; everything else unchanged |
| nothing in the chain under `faults.Enrich` says its datasource | `enricher.declare` → `crud.SourceOf` | the process refuses to start, naming the fix — a decorator that forwards neither `Source` nor `Next`, or `faults.WithSource` |

## Files

| File | Role |
|---|---|
| `crud/probe/doc.go` | the rules no signature carries: the three codes and the four named refusals, the transaction matrix, the cap numbers, the oracle controls, why the duplicate map is the one correct Go-side check |
| `crud/probe/probe.go` | `Handler`, `Declarer`, `Savepointer`, `Request`, `Row`, `Simple` |
| `crud/probe/full.go` | `Full`, `full`, `Enrich`, `runs`, `run`, `merge`, `same`, `fold`, `violation`, `path`, `valueOf`, `truthy` |
| `crud/probe/plan.go` | `candidate`, `term`, `plan`, `candidatesFor`, `reproducible`, `restricting`, `planFor`, `modeFor`, `swallowed`, `bind` |
| `crud/probe/sql.go` | `ref`, `statement`, `renderTerm`, `renderUnique`, `renderForeignKey`, `renderRestrict`, and the five aliases |
| `crud/probe/dup.go` | `finding`, `duplicates`, `keyOf`, `render` — the map, and [[D-025]]'s bug written down rather than repeated |
| `crud/probe/options.go` | `Option`, `WithSavepoints`, `WithScope`, `WithValues`, `CodeOnly`, `Skip`, `WithMaxConstraints`, `WithMaxRows`, `WithTimeout`, `WithMaxSavepoints`, and the four `Default*` numbers |
| `crud/probe/declare.go` | `Declare`, `identifies`, and the four sentinels |
| `crud/decorators/faults/probe.go` | `Option`, `WithProbe`, `WithProbeFor`, `WithSource`, `WithProbeError`, `probeCfg`, `declare`, `probed`, `enrichProbed`, `savepoint`, `insertRequest`, `updateRequest` |
| `crud/decorators/faults/faults.go` | `Enrich`, `enricher`, `enrich`, `finish`, `resolve`, `resolvePath`, `Next`, and the three probed verbs |
| `crud/executor.go` | `binding.owned`, `binding.saves`, `push`, `OwnedExecutorFor`, `ClaimSavepoint`, `bindingFor`, `Sourced`, `Nexter`, `SourceOf`, `BeginnerOf` — the last three are what let the probe sit anywhere in the chain ([[D-061]]) |
| `crud/dialect.go` | `UpsertScope`, `StatementRollback`, and their implementations |
| `crud/update.go` | `DefinedChanges` — `DefinedFields` with the values as well as the names |
| `crud/render.go` | `SQL` — the builder every term is rendered through |
| `crud/sqlrepo/repository.go` | `repository.Source` — three lines, and the whole of `crud.Sourced` |
| `crud/catalog/catalog.go` | `Referrers` — the inbound direction of the schema |
| `crud/catalog/load.go` | `snapshot.refs`, `loaded.ReferencedBy` |
| `errs/violation.go` | `SortViolations` — the order the answer is returned in |
| `errs/fault.go` | `Fault.Partial` — what a capped or failed probe sets |
| `test/integration/probe_schema_test.go` | the live fixture, and why the hard/easy twin is per engine |
| `test/integration/probe_test.go` | the live suite |

## Tests that walk this flow

- `TestOneFailedWriteBecomesEveryViolationItCaused` — `test/integration/probe_test.go` — probe off ⇒ one violation, probe on ⇒ three distinct codes at three distinct paths, on five targets with a counter. The assertion is on the *set* of `(code, path)` pairs, because counting to three passes for one violation repeated.
- `TestAPayloadWithOneRealViolationYieldsExactlyOne` — `test/integration/probe_test.go` — the negative twin: a NULL nullable foreign key, a composite foreign key with one NULL column, a NULL half of a composite unique key and the unreproducible key's bait, all beside one real violation.
- `TestTheSamePayloadWithARealMissingParentYieldsTwo` — its control: a probe that closed the NULL hole by dropping foreign keys altogether fails here.
- `TestAnUpdateDoesNotReportARowCollidingWithItself` — `test/integration/probe_test.go` — the own-row exclusion and the restrict change-guard, on one payload.
- `TestTheUnreproducibleKeyIsNeverProbedAndItsPlainTwinIs` — `test/integration/probe_test.go` — the twin, live: partial on PostgreSQL and SQLite, a prefix key on MySQL and MariaDB.
- `TestPastTheCapTheAnswerSaysItIsIncomplete` — with the cap raised as its control.
- `TestAProbeThatErrorsKeepsTheConflict` — the catalog naming a table the database no longer has, with the healthy twin as its control; both are 409 and neither is 500.
- `TestTheOffendingValueReachesTheBodyOnlyWhenAsked` — the same write rendered twice, through a message template naming `{value}`.
- `TestTheTransactionMatrix` — twenty arms with a counter: outside, inside a transaction vv opened with and without savepoints, and inside a foreign one.
- `TestABulkWriteAttributesEachViolationToItsRow` — row attribution and the intra-payload duplicate, live.
- `TestADeclarationAgainstACatalogWithoutTheTableRefusesToStart` — [[D-041]]'s owed refusal, with the known table as its control.
- `TestTheSameFailingRequestTwiceProducesTheSameBody` — byte-identical over five renders, with a count of violations in the body as the control that the comparison measures an order.
- `crud/probe/full_test.go` — the unit half: a probe that finds nothing, one that errors, the NULL guards and their controls, positional reads against two 61-character names, the dialect's placeholders, the upsert skip set per engine, the caps, the timeout, the scope predicate, code-only mode and the merge rule with both its controls.
- `crud/probe/dup_test.go` — row attribution, the duplicate map and the statement count beside it, an unkeyable row and its comparable twin.
- `crud/probe/declare_test.go` — each refusal with the declaration that starts beside it, and the transaction matrix as a unit table with a counter.
- `crud/probe/probe_test.go` — `Simple`: no statement, the driver's violation unchanged, and `Full` over the same request issuing one as the control.
- `crud/decorators/faults/probe_test.go` — the per-verb defaults, the field hop, the bind-time refusals, the savepoint budget, the foreign transaction, and the probe error reaching `WithProbeError` and not the client.
- `crud/executor_test.go:TestATransactionVVOpenedIsMarkedOwnedAndAForeignOneIsNot` / `:TestASavepointClaimCountsPerTransactionAndNotPerRepository` / `:TestNoSavepointIsClaimedInAForeignTransactionOrOutsideOne`.
- `crud/dialect_test.go:TestOnlyADialectThatSaysSoSwallowsThePrimaryKeyOnly` / `:TestOnlyADialectThatSaysSoRollsBackTheStatementAlone` — each with a dialect implementing neither interface as its control.
- `crud/update_test.go:TestDefinedChangesCarriesTheValuesDefinedFieldsOnlyNames`.

## See also

[[FL-014]] [[FL-011]] [[FL-009]] [[FL-016]] [[FL-008]] [[UC-017]] [[UC-015]] [[UC-004]]
[[D-042]] [[D-041]] [[D-019]] [[D-009]] [[D-010]] [[D-011]] [[D-014]] [[D-032]]
