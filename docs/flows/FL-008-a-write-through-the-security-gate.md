# FL-008 — A write through the security gate

**Entry point:** `crud/decorators/security/security.go:gate.Save` / `:gate.Update` / `:gate.Delete` / `:gate.DeleteAll` / `:gate.UpdateAll`
**Implements:** [[UC-004]] [[UC-008]] · **Governed by:** [[D-008]] [[D-004]] [[D-011]]

Reads have one shape. Writes have five, and each one has a different reason it
cannot simply AND a predicate into a statement.

## Save — the unscoped-existence probe

1. **`gate.Save`** — `crud/decorators/security/security.go:337`
   `meta.HasID(m)` decides which action is being attempted, exactly as
   `repository.Save` does ([[FL-003]]). With no id it is a `Create`; with one it
   might be either, and the gate has to find out.

2. **`gate.saveTarget`** — `security.go:392`
   Two lookups, and the second is the point:
   ```go
   found := Core.GetAll(Where(scope), byID, rel, Limit(1), Unsorted())  // scoped
   if len(found) == 1 { return &found[0] }        // an overwrite you may do
   if scope == nil    { return nil, nil }         // a plain insert
   hidden := Core.Exists(ctx, byID)               // UNSCOPED, on purpose
   if hidden { return nil, Denied(Update, "row is outside the scope") }
   return nil, nil                                // genuinely absent: an insert
   ```
   **Why unscoped.** `Save` is an upsert: there is no `WHERE` clause for the
   scope to narrow, so refusing is the only move the gate has. Left alone, a
   policy that scoped rows and nothing else gave `Save` no protection at all —
   the insert turned into an update and re-tenanted somebody else's row with
   `err == nil`.
   The probe leaks one bit (this id is taken) to a caller who already chose the
   id. The alternative leaks the row.

3. **`checkImmutableSave`** — `security.go:430`
   Compares the frozen fields **by value** between the stored row and the
   incoming one. This is deliberately different from `Update`: a `Save` carries
   the whole row, so every field is "provided", and judging by definition would
   refuse every save.

4. `authorize(Create|Update)`, then `inspect(action, m)` on the **incoming**
   model (`security.go:377`) — this is what catches a row being written *into*
   somebody else's scope — then `Core.Save`.

## Update — the scope in both halves

1. **`gate.Update`** — `security.go:454`
   `authorize(Update)`, then the frozen-field check via `crud.DefinedFields`
   (`crud/update.go:314`), which builds and caches an `UpdatePlan` for this DTO
   type from the `Meta` alone. It refuses a field the DTO **defines**, even when
   the value is unchanged — the PATCH said "set this", and the answer is no.
2. `loadScoped` (`security.go:238`) — an out-of-scope id is `ErrNotFound`, same
   as a read ([[FL-007]]).
3. `inspect(Update, &cur)` on the stored row.
4. `Core.Update(ctx, id, dto, Where(scope), rel, opts…)` — **the scope is passed
   as an option, not just checked here.** `repository.Update` puts options into
   the load *and* into the UPDATE's own `WHERE`
   (`crud/sqlrepo/repository.go:540`, `:584`). Checking here and writing unscoped
   was check-then-act: a row that left the scope in between was updated anyway,
   and a fresh copy of somebody else's record was handed back with `err == nil`.

## UpdateAll and DeleteAll — the unscoped refusal

1. **`gate.UpdateAll`** — `security.go:499` · **`gate.DeleteAll`** —
   `security.go:569`
   `gate.scoped` returns the assembled options *and* the scope predicate, and
   the refusal reads:
   ```go
   if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedUpdateAll {
       return 0, Denied(Update, "refusing an unscoped UpdateAll; …")
   }
   ```
   Neither the policy nor the caller narrowed the statement, so it would touch
   every row. The two permissions are separate flags on purpose
   (`security.go:102-110`): rewriting every row in a table is not something a
   policy should inherit from having been allowed to empty it.
2. **The victim scan.** When `Inspect` is set, both methods fetch the rows the
   statement is about to hit — through `g.whole(true, scoped)`, so `Inspect`
   sees whole rows — and run `Inspect` on each before issuing the write. With no
   id in the call there is nothing else that could stand for consent.
3. Then the statement, with the scope in it: `Core.UpdateAll(dto, scoped…)` /
   `Core.DeleteAll(scoped…)`.

## Delete — by id, but not by `Delete`

**`gate.Delete`** — `security.go:535`
There is a fast path: with no scope and no `Inspect` it forwards to
`Core.Delete(ids…)`, which still ANDs the *blueprint's* scope
(`crud/sqlrepo/repository.go:720`).
Otherwise it builds `within = And(scope, InAny(pk, ids))`, optionally scans the
victims and inspects them, and then calls **`Core.DeleteAll(Where(within))`**,
not `Core.Delete`. That is the whole trick: `Core.Delete` takes ids and no
options, so there is nowhere to put the policy's predicate;
`DeleteAll` takes options and puts them in the statement. Rows outside the
scope are simply not matched, so the reported count is honest.

## Where the decisions bite

- **Every write puts the policy in the statement, not only in a check.** Save is
  the exception that proves it: it has no `WHERE`, so it gets a probe and a
  refusal instead.
- **Frozen fields are judged by definition on `Update` and by value on `Save`.**
  Both are correct for their shape; swapping them breaks one or the other.
- **An unscoped bulk write is refused by default.** Opting in is two separate
  flags, and `Combine` of no policies produces neither ([[FL-007]]).
- **`Inspect` sees whole rows.** Every place that hands rows to `Inspect` goes
  through `gate.whole` (`security.go:209`), which appends `crud.SelectAll()`.
- **Delete is re-expressed as DeleteAll.** Anything that "simplifies" it back to
  `Core.Delete(ids…)` drops the policy scope from the statement while keeping the
  check in front of it — a row hidden from reads becomes deletable by id.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `Save` overwriting a row outside the scope | `saveTarget` (`security.go:423`) | 403 |
| `Save` changing a frozen field | `checkImmutableSave` (`security.go:448`) | 403 |
| `Save` writing a row into another scope | `inspect(action, m)` (`security.go:377`) | 403 |
| PATCH naming a frozen field | `gate.Update` (`security.go:466`) | 403, before any SQL |
| PATCH on an out-of-scope id | `loadScoped` | 404 |
| row leaves the scope between load and write | the scope in the UPDATE's `WHERE` → `missedRow` | 404 |
| `UpdateAll`/`DeleteAll` with nothing narrowing them | `security.go:518`, `security.go:577` | 403 with the flag named |
| `Inspect` refuses one victim | the scan loops (`security.go:527`, `:561`, `:586`) | 403, and no statement is issued |
| policy is `ReadOnly` | `Authorize` (`policies.go:127`) | 403 |
| `nil` model to `Save` | `gate.Save` (`security.go:339`) | 403 |

## Files

| File | Role |
|---|---|
| `crud/decorators/security/security.go` | every write method, `saveTarget`, `checkImmutableSave`, `whole`, `scoped` |
| `crud/decorators/security/policies.go` | `ScopeField` (scope + `Inspect` + frozen field), `ReadOnly`, `Freeze`, `Combine` |
| `crud/update.go` | `DefinedFields` — the frozen check without a typed DTO |
| `crud/access.go` | `HasID`, `ID`, `Values`, `ElemValue` |
| `crud/sqlrepo/repository.go` | `Update` (options in both halves), `Delete`, `DeleteAll`, `UpdateAll` |
| `crud/options.go` | `Where` accumulating, which is what makes a prepended scope unremovable |

## Tests that walk this flow

- `TestSaveRefusesToWriteIntoAnotherTenant` — `crud/decorators/security/security_test.go`.
- `TestAScopeWithoutInspectStillRefusesAnOverwriteOfAHiddenRow` — `crud/decorators/security/gate_edge_test.go` — the unscoped probe.
- `TestAScopedSaveOfAnUnusedIDIsStillAnInsert` — `crud/decorators/security/gate_edge_test.go` — the other branch of the probe.
- `TestSaveWithoutAPrincipalWritesNothing` — `crud/decorators/security/edge_test.go`.
- `TestSaveJudgesAFrozenFieldByItsValue` — `crud/decorators/security/edge_test.go`.
- `TestAFrozenFieldIsRefusedOnUpdateEvenWhenTheValueIsUnchanged` — `crud/decorators/security/edge_test.go` — the asymmetry.
- `TestFreezeRefusesAnUpdateThatNamesAFrozenField` — `crud/decorators/security/gate_edge_test.go`.
- `TestTheGateScopeIsInTheUpdatesOwnWhereClause` — `crud/decorators/security/gate_edge_test.go`.
- `TestAnUpdateOfARowThatLeftTheScopeIsNotFound` — `crud/decorators/security/gate_edge_test.go`.
- `TestUpdateIsScopedAndFreezesTheScopeField` — `crud/decorators/security/security_test.go`.
- `TestDeleteIsScoped` — `crud/decorators/security/security_test.go` — Delete re-expressed as DeleteAll.
- `TestUnscopedDeleteAllIsRefused` — `crud/decorators/security/security_test.go`.
- `TestUpdateAllIsScopedInTheStatementItself` — `crud/decorators/security/updateall_test.go`.
- `TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllRefusesAFrozenField` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllInspectsEveryRowItIsAboutToWrite` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllIsRefusedByAReadOnlyPolicy` — `crud/decorators/security/updateall_test.go`.
- `TestInspectAbortsTheWholeCall` — `crud/decorators/security/edge_test.go`.
- `TestARowHiddenFromReadsIsStillDeletableByID` — `crud/http/crudfiber/write_edge_test.go` — what happens *without* a gate, and why `WithScope` is not one.

## See also

[[FL-007]] [[FL-002]] [[FL-003]] [[FL-011]]
