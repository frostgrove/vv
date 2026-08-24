# FL-002 — A PATCH becomes an UPDATE

**Entry point:** `http/crudfiber/handler.go:Update`
**Implements:** [[UC-003]] [[UC-009]] · **Governed by:** [[D-002]] [[D-010]] [[D-019]]

The read-modify-write that most hand-written handlers get wrong. Three states in
the body, a diff against the stored row, one statement, and a read-back that
differs by dialect.

## The path

1. **`Handler.Update`** — `http/crudfiber/handler.go:221`
   `h.id(c)` coerces the path parameter to `ID` (`handler.go:367`, via
   `query.Coerce`), then `c.Bind().Body(&dto)` decodes the body into `U`.
   `BeforeUpdate` runs here if configured (`http/crudfiber/options.go:67`).
   **The trap:** the call is `h.repo.Update(ctx, id, dto)` — no options. A
   `WithScope` narrowing reaches every read and *nothing* on this path. Row-level
   rules on writes belong in `security.Gate`, whose scope does reach the UPDATE
   ([[FL-008]]); the asymmetry is documented at `http/crudfiber/options.go:47`.

2. **`crud.Repo.Update`** — `crud/repo.go:71`
   The typed façade. It only re-types `dto U` down to `any` for the `Core`
   chain; every decorator sees `any` and the compiler still checks the call site.

3. **(optional) `gate.Update`** — `repo/decorators/security/security.go:454`
   When the repository was bound with a gate. Frozen-field check, a scoped load,
   `Inspect`, then the scope goes into the options it forwards — see [[FL-008]].

4. **`repository.Update`** — `repo/basic/repository.go:531`
   The whole write lives here. In order:

5. **The load** — `repository.go:544-556`
   Options are `Where(PK = id)` + the caller's + `Limit(1)` + `Unsorted()`. Then:
   ```go
   if _, inTx := crud.ExecutorFor(ctx, r.src); inTx {
       loadOpts = append(loadOpts, crud.ForUpdate())
   }
   ```
   **The FOR UPDATE branch.** The row is locked only when the context already
   carries an executor this repository would run on — i.e. somebody opened a
   transaction. Outside one, `SELECT … FOR UPDATE` would take a lock and drop it
   before the UPDATE, which is worse than useless. `LockClause` is the dialect's
   (`crud/dialect.go:23`); SQLite renders nothing, because it has no row locks.
   The load goes through `GetAll`, so the repository's own `Scope` and any
   caller predicate narrow it. No row → `ErrNotFound`.

6. **`UpdatePlan.Changes`** — `crud/update.go:188`
   The diff. Each DTO field is read through `planField.read`
   (`crud/update.go:152`), which is where the **three states** live:
   | plan kind | DTO field type | undefined | defined |
   |---|---|---|---|
   | `planPlain` | `T` | impossible — always written | value |
   | `planPtr` | `*T` | `nil` → skipped | `*p` |
   | `planOpt` | `crud.Opt[T]` | not defined → skipped | value, or `nil` meaning SQL NULL |
   Then `valuesEqual(pf.Target.comparableOf(base), val)` (`crud/update.go:205`)
   drops any column whose value is already there. `comparableOf`
   (`crud/meta.go:66`) normalises both `undefined` and `null` to Go `nil`, and
   `time.Time` compares by instant, not by wall clock and monotonic reading.
   The plan itself was built and validated at `Define` time — see [[FL-004]].

7. **The empty-change shortcut** — `repository.go:562-564`
   No changes → the loaded row is returned and **no statement is issued**. With
   a version column this also means the counter does not advance, which is
   correct: nothing changed.

8. **Statement assembly** — `repository.go:566-584`
   `UPDATE … SET col = ?, …`. Then the version, in its two halves:
   ```go
   stale, err := r.versionCheck(&cur)          // repository.go:634 → Eq(version, valueAsRead)
   ... SET version = version + 1               // repository.go:582
   ... WHERE PK = id AND <caller narrowing> AND <stale>   // repository.go:584
   ```
   The counter goes up so anyone else holding the row knows their copy is old,
   and the value it had when we read it goes into the WHERE, so a concurrent
   writer makes this statement match nothing instead of being overwritten.
   The caller's narrowing goes into **both** halves — the load and this WHERE.
   Checking it only on the load is check-then-act: a row that leaves the
   narrowing in between gets written anyway and handed back to a caller who was
   never allowed to see it.

9. **The RETURNING branch** — `repository.go:586-603`
   PostgreSQL and SQLite. One round trip: `UPDATE … RETURNING <all columns>`,
   scanned straight back into `cur`. No rows returned → `missedRow`.

10. **The no-RETURNING branch (MySQL)** — `repository.go:605-629`
    Two subtleties, both learned the hard way:
    - MySQL reports 0 rows affected for a write that changed nothing, so
      `RowsAffected` alone cannot tell "no such row" from "nothing to do".
    - Patching the loaded row in memory instead of re-reading reported success —
      with a fabricated model — for a row deleted between the load and the write.
    So: **with** a version column, `RowsAffected == 0` is trustworthy (the
    counter is always one of the changes, so every matching row is changed) and
    goes to `missedRow`. Then, always, `repository.refresh` (`repository.go:506`)
    re-reads the row through `PK = id AND <narrowing>` — a write allowed to touch
    only some rows must not read back a row it was not allowed to touch.

11. **`repository.missedRow`** — `repository.go:650`
    Explains a statement that matched nothing. Without a version: `ErrNotFound`.
    With one: `Exists` under the same narrowing decides between `ErrNotFound`
    (the row is gone; stop) and `ErrStaleVersion` (the row moved on; read it
    again and reapply). `ErrStaleVersion` wraps `ErrConflict`, so a transport
    answers 409 without knowing versions exist (`crud/errors.go:36`).

12. **`Handler.entity`** — `handler.go:407` — 200 with the refreshed model, or
    the `WithTransform` presenter's view of it.

## Where the decisions bite

- **Three states, or the feature is gone.** `planField.read` is the only place a
  DTO field's intent is decided. Collapse `Opt[T]` to `*T` and either "write
  NULL" or "leave alone" becomes unreachable ([[D-002]]).
- **Only what changed is written.** The diff in `Changes` is what keeps a PATCH
  from touching columns a trigger or another writer owns. `UpdateAll` is the
  deliberate exception — no single row to diff against, so it uses
  `UpdatePlan.Writes` (`crud/update.go:219`) instead.
- **The narrowing is in the WHERE, not only in the load.** Both halves, always.
  This is the invariant a decorator relies on; `gate.Update` passes its scope as
  an option precisely because `repository.Update` puts options into the
  statement (`repository.go:540`, `repository.go:584`).
- **The version column is never the caller's.** It is excluded from
  `Schema.Update` (`crud/meta.go:293`) and an update DTO that names it is
  refused at `Define` time (`crud/update.go:119`).
- **The read-back is not an optimisation to remove.** On a dialect without
  RETURNING it is the only thing that keeps `Update`'s promise that the returned
  model describes the row.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `:id` does not parse as `ID` | `Handler.id` (`handler.go:367`) | 400 `"…is not a valid id"` |
| malformed JSON body | `c.Bind().Body` → `badRequest` | 400 |
| DTO field names a column the model lacks | `PlanFor` at `Define` time | panic at start-up, not a request |
| no row with that id, or it is outside the narrowing | the load (`repository.go:553`) | 404 |
| row deleted between the load and the write | `missedRow` → `ErrNotFound` | 404 |
| row changed by somebody else (version) | `missedRow` → `ErrStaleVersion` | 409 |
| a `NOT NULL` / FK / unique violation from the SET | adapter `conflict()` | 409 — [[FL-011]] |
| DTO of the wrong type reaches `Changes` | `UpdatePlan.dtoValue` (`update.go:174`) | 400 (`SchemaError`) |

## Files

| File | Role |
|---|---|
| `http/crudfiber/handler.go` | `Update`, id coercion, response |
| `http/crudfiber/options.go` | `BeforeUpdate`, and the `WithScope` asymmetry |
| `crud/repo.go` | the typed `Update` façade |
| `crud/update.go` | `UpdatePlan`, `Changes`, `planField.read`, `valuesEqual` |
| `crud/opt.go` | the three states themselves |
| `crud/meta.go` | `Field.comparableOf`, `Schema.Update`, `checkVersion` |
| `repo/basic/repository.go` | `Update`, `versionCheck`, `missedRow`, `refresh` |
| `crud/dialect.go` | `SupportsReturning`, `LockClause` |
| `crud/executor.go` | `ExecutorFor` — the "are we in a transaction" question the `FOR UPDATE` branch asks |
| `repo/decorators/security/security.go` | the gated variant |

## Tests that walk this flow

- `TestUpdateForwardsOnlyTheFieldsTheBodyCarried` — `http/crudfiber/handler_test.go` — the wire half.
- `TestUpdateCarriesAnExplicitNullThrough` — `http/crudfiber/handler_test.go` — `null` is not absence.
- `TestUpdateWritesOnlyChangedFields` — `repo/basic/repository_test.go` — the diff.
- `TestUpdateWithNothingToDoSkipsTheWrite` — `repo/basic/repository_test.go` — no statement at all.
- `TestUpdateDistinguishesUndefinedFromNull` — `repo/basic/repository_test.go` — the three states in SQL.
- `TestUpdateOnADialectWithoutRETURNINGReadsTheRowBack` — `repo/basic/repository_test.go` — the MySQL re-read.
- `TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect` — `repo/basic/repository_test.go` — pins the fabricated-model regression.
- `TestUpdateChecksTheVersionItReadAndAdvancesIt` — `repo/basic/version_test.go` — both halves of the lock.
- `TestAnUpdateAgainstARowSomebodyElseChangedIsRefused` — `repo/basic/version_test.go`.
- `TestAVanishedRowIsStillNotFoundRatherThanStale` — `repo/basic/version_test.go` — `missedRow`'s two answers.
- `TestAnUpdateWithNothingToDoLeavesTheVersionAlone` — `repo/basic/version_test.go`.
- `TestUpdateLoadsThroughTheScopeSoAnOutsideRowIsNotFound` — `repo/basic/blueprint_edge_test.go`.
- `TestAConcurrentWriteIsRefusedRatherThanLost` — `test/integration/dialect_edge_test.go` — against real engines.
- `TestForUpdateMakesTwoTransactionsTakeTurns` — `test/integration/edge_test.go` — the lock branch.
- `TestATimeThatOnlyDiffersInItsClockReadingIsNotAChange` — `crud/edge_test.go` — `valuesEqual` on `time.Time`.

## See also

[[FL-003]] [[FL-004]] [[FL-008]] [[FL-009]] [[FL-011]]
