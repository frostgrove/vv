# FL-008 — A write through the security gate

**Entry point:** the write methods on `crud/decorators/security/security.go:gate`, including `InsertBatch`
**Implements:** [[UC-004]] [[UC-008]] · **Governed by:** [[D-008]] [[D-004]] [[D-011]] [[D-026]] [[D-030]] [[D-079]] [[D-083]] [[D-087]]

Reads have one shape. Writes have several, and each has a different reason it
cannot simply AND a predicate into a statement.

## Save — the unscoped-existence probe

1. **`gate.Save` → `gate.save`** — `crud/decorators/security/security.go:save`
   `Save` and `SaveOnly` copy the caller's model and hand it to one body, so a
   refusal never leaves the caller's own value half-written.
   A scope with no `Inspect` is refused here, before anything is read: an upsert
   carries a whole row, and a rule that can narrow but cannot judge has nothing
   to say about the values coming in. Then `meta.HasID(m)` decides which action
   is being attempted, exactly as `repository.Save` does ([[FL-003]]). With no id
   it is a `Create`; with one it might be either, and the gate has to find out —
   so it authorises **both** `Create` and `Update` before it looks, because the
   lookup itself would otherwise tell an unauthorised caller whether the id is
   taken.

2. **`gate.saveTarget`** — `crud/decorators/security/security.go:saveTarget`
   Two lookups, and the second is the point:
   ```go
   byID  := Where(Eq(pk, id))
   found := Core.GetAll(whole(Where(scope), byID, rel, Limit(1), Unsorted(), PrimaryOnly()))
   if len(found) == 1 { return &found[0], nil }   // an overwrite you may do
   if scope == nil    { return nil, nil }         // a plain insert

   hidden, err, supported := crud.ExistsUnscopedOf(Core, ctx, byID, PrimaryOnly())
   if !supported || hidden { return nil, crud.ErrNotFound }
   return nil, nil                                // genuinely absent: an insert
   ```
   **Why unscoped.** Left alone, a policy that scoped rows and nothing else gave
   `Save` no protection at all — the insert turned into an update and re-tenanted
   somebody else's row with `err == nil`. The scoped lookup cannot tell "not
   yours" from "not there", and those two need opposite statements.
   **Why the answer is `crud.ErrNotFound` and not a denial.** A 403 would confirm
   the row exists, which is exactly what [[D-008]] refuses; the probe's one bit is
   read by the gate and not handed back. A core that cannot answer the unscoped
   question fails the same way rather than falling through to an insert that would
   become an overwrite.

3. **`checkImmutableSave`** — `crud/decorators/security/security.go:checkImmutableSave`
   Compares the frozen fields **by value** between the stored row and the
   incoming one. This is deliberately different from `Update`: a `Save` carries
   the whole row, so every field is "provided", and judging by definition would
   refuse every save. `inspect(Update, existing)` runs first, on the **stored**
   row, so a rule that refuses the row as it stands refuses before the freeze is
   even consulted.

4. `inspect(action, m)` on the **incoming** model
   (`crud/decorators/security/security.go:inspect`) — this is what catches a row
   being written *into* somebody else's scope.

5. **`gate.saveScoped`** / **`gate.saveScopedOnly`** —
   `crud/decorators/security/security.go:saveScoped`
   With an id, the write is not `Core.Save`. The gate hands the whole decision
   down as a `crud.ScopedSave`: the row the probe saw as `Previous`, plus its own
   scope and relation scopes. `repository.saveScopedUpdate`
   (`crud/sqlrepo/repository.go:saveScopedUpdate`) turns that into an `UPDATE …
   WHERE pk = ? AND <blueprint scope> AND <gate scope> AND <every column equals
   what the probe read>`, and `repository.saveScopedCreate` turns the other branch
   into a create-only insert. So the window between the probe and the write is
   closed by the statement rather than by a check, and a miss comes back as
   `crud.ErrNotFound` — or, on the create branch, as `ErrCreateRaced`, which the
   gate reports as `Denied(Create, "assigned key was concurrently created")`.
   A core that does not implement the verb is refused rather than written through
   unscoped.

## InsertBatch — create-only and exact

`gate.InsertBatch` copies every command, resolves its scope and relation-scope
preconditions once, authorises `Create`, then inspects every row as `Create`
before any storage call.
An assigned key never changes the action to Update because the repository verb
is insert-only. A scope or relation scope without `Inspect` is refused: an
INSERT has no WHERE clause in which to enforce it.

The gate invokes `crud.InsertBatchOf` on its exact inner Core. It does not walk
through a decorator that may own another validation or audit obligation. That
layer must implement the optional verb explicitly or the call fails closed with
`ErrNoBatchInsertSupport`. Batch options are forwarded unchanged; sqlrepo alone
selects native COPY or portable SQL ([[D-083]]).

## Update — the scope in both halves

1. **`gate.Update`** — `crud/decorators/security/security.go:Update`
   `authorize(Update)`, then the frozen-field check via `crud.DefinedFields`
   (`crud/update.go:DefinedFields`), which builds and caches an `UpdatePlan` for this DTO
   type from the `Meta` alone. It refuses a field the DTO **defines**, even when
   the value is unchanged — the PATCH said "set this", and the answer is no.
2. `loadScopedWith` (`crud/decorators/security/security.go:loadScopedWith`) — an
   out-of-scope id is `ErrNotFound`, same as a read ([[FL-007]]). It runs only
   when the policy has an `Inspect`; with a scope alone there is nothing to judge
   and the narrowing is already in the statement below.
3. `inspect(Update, &cur)` on the stored row.
4. `Core.Update(ctx, id, dto, Where(scope), rel, opts…)` — **the scope is passed
   as an option, not just checked here.** `repository.Update` puts options into
   the load *and* into the UPDATE's own `WHERE`
   (`crud/sqlrepo/repository.go:Update`). Checking here and writing unscoped
   was check-then-act: a row that left the scope in between was updated anyway,
   and a fresh copy of somebody else's record was handed back with `err == nil`.

## UpdateAll and DeleteAll — the unscoped refusal

1. **`gate.UpdateAll`** — `crud/decorators/security/security.go:UpdateAll` ·
   **`gate.DeleteAll`** — `crud/decorators/security/security.go:DeleteAll`
   `gate.scoped` returns the assembled options *and* the scope predicate, and
   the refusal reads:
   ```go
   if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedUpdateAll {
       return 0, Denied(Update, "refusing an unscoped UpdateAll; …")
   }
   ```
   Neither the policy nor the caller narrowed the statement, so it would touch
   every row. The two permissions are separate flags on `Policy`
   (`crud/decorators/security/security.go:Policy`) on purpose: rewriting every
   row in a table is not something a policy should inherit from having been
   allowed to empty it.
2. **The victim scan.** When `Inspect` is set, both methods fetch the rows the
   statement is about to hit — through `g.whole(true, append(scoped,
   crud.PrimaryOnly(), inspectionRead()))`, so `Inspect` sees whole rows, from
   the primary, and *all* of them: `inspectionRead` zeroes the paging and the
   cursors so the scan cannot see a page of what the statement will write
   ([[D-026]]). Then `Inspect` runs on each row before the write is issued. With
   no id in the call there is nothing else that could stand for consent.
3. Then the statement, with the scope in it: `Core.UpdateAll(dto, scoped…)` /
   `Core.DeleteAll(scoped…)`. The caller's own paging never got a second chance
   to matter: the repository refuses it there ([[D-087]]), so a caller who
   passed `crud.Limit(1)` gets a `*crud.SchemaError` rather than a whole-table
   write.

## Delete — by id, but not by `Delete`

**`gate.Delete`** — `crud/decorators/security/security.go:Delete`
There is a fast path: with no scope and no `Inspect` it forwards to
`Core.Delete(ids…)`, which still ANDs the *blueprint's* scope
(`crud/sqlrepo/repository.go:Delete`). If the id set crosses the dialect bind
budget, that storage path repeats the blueprint and relation scopes in every
chunk and executes the chunks in one transaction; a chunk boundary never
widens what the repository hides ([[D-079]], [[FL-009]]).
Otherwise it builds `within = And(scope, InAny(pk, ids))`, optionally scans the
victims and inspects them, and then calls **`Core.DeleteAll(Where(within))`**,
not `Core.Delete`. That is the whole trick: `Core.Delete` takes ids and no
options, so there is nowhere to put the policy's predicate;
`DeleteAll` takes options and puts them in the statement. Rows outside the
scope are simply not matched, so the reported count is honest.

## Where the decisions bite

- **Every write puts the policy in the statement, not only in a check.** `Save`
  was the exception and stopped being one: the upsert became a scoped verb, so
  the gate's own scope is in the `UPDATE`'s `WHERE` beside the blueprint's. What
  is left of the exception is the probe, which decides *which* statement to
  write, not whether the write is allowed.
- **Frozen fields are judged by definition on `Update` and by value on `Save`.**
  Both are correct for their shape; swapping them breaks one or the other.
- **An unscoped bulk write is refused by default.** Opting in is two separate
  flags, and `Combine` of no policies produces neither ([[FL-007]]).
- **`Inspect` sees whole rows.** Every place that hands rows to `Inspect` goes
  through `gate.whole` (`crud/decorators/security/security.go:whole`), which
  appends `crud.SelectAll()`.
- **Delete is re-expressed as DeleteAll.** Anything that "simplifies" it back to
  `Core.Delete(ids…)` drops the policy scope from the statement while keeping the
  check in front of it — a row hidden from reads becomes deletable by id.
- **A filtered write is all-or-nothing, and says so.** The scan sees every
  matching row and the statement writes every matching row; an option that
  suggests otherwise is refused by the repository rather than dropped
  ([[D-026]], [[D-087]]).
- **Storage chunking repeats the declaration-time scope.** The gate's direct
  fast path may become several statements only at a dialect bind boundary; the
  repository preflights all of them and shares one transaction ([[D-079]]).
- **InsertBatch is always Create.** Assigned ids conflict rather than becoming
  authorised overwrites, and every row is checked before native or portable I/O.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `Save` overwriting a row the scope hides | `gate.saveTarget` | **404** — the probe's answer is not handed back ([[D-008]]) |
| the core cannot answer the unscoped probe | `gate.saveTarget` | **404** — it fails closed rather than inserting |
| a row leaves the scope between the probe and the write | the gate's scope in the scoped upsert's `WHERE` | 404 |
| an assigned key is created concurrently | `gate.saveScoped` | 403, `assigned key was concurrently created` |
| the core has no scoped-upsert verb | `gate.saveScoped` | 403, rather than an unscoped write |
| `Save` changing a frozen field | `gate.checkImmutableSave` | 403 |
| `Save` writing a row into another scope | `inspect(action, m)` | 403 |
| a scope-only policy reaches `Save` or `Update` | `gate.save`, `gate.Update` | 403 naming the missing `Inspect` |
| PATCH naming a frozen field | `gate.Update` | 403, before any SQL |
| PATCH on an out-of-scope id | `gate.loadScopedWith` | 404 |
| row leaves the scope between load and write | the scope in the UPDATE's `WHERE` → `missedRow` | 404 |
| `UpdateAll`/`DeleteAll` with nothing narrowing them | `gate.UpdateAll`, `gate.DeleteAll` | 403 with the flag named |
| `Inspect` refuses one victim | the scan loops in `gate.UpdateAll`, `gate.Delete`, `gate.DeleteAll` | 403, and no statement is issued |
| policy is `ReadOnly` | `Authorize` (`crud/decorators/security/policies.go:ReadOnly`) | 403 |
| `nil` model to `Save` | `gate.Save` | 403 |
| one `InsertBatch` row fails `Inspect` | `gate.InsertBatch` preflight | 403, no row written |
| a scope-only policy cannot validate an inserted row | `gate.InsertBatch` | 403 naming the missing Inspect |
| an inner decorator did not preserve the optional verb | exact capability check | `ErrNoBatchInsertSupport`, no I/O |

## Files

| File | Role |
|---|---|
| `crud/decorators/security/security.go` | every write method, `saveTarget`, `saveScoped`, `checkImmutableSave`, `whole`, `scoped` |
| `crud/decorators/security/policies.go` | `ScopeField` (scope + `Inspect` + frozen field), `ReadOnly`, `Freeze`, `Combine` |
| `crud/update.go` | `DefinedFields` — the frozen check without a typed DTO |
| `crud/access.go` | `HasID`, `ID`, `Values`, `ElemValue` |
| `crud/sqlrepo/repository.go` | `Update` (options in both halves), `saveScopedUpdate`, `saveScopedCreate`, `Delete`, `DeleteAll`, `UpdateAll` |
| `crud/executor.go` | `ScopedSave`, `SaveScopedOf`, `ExistsUnscopedOf` — the optional verbs the gate needs and refuses without |
| `crud/options.go` | `Where` accumulating, which is what makes a prepended scope unremovable |
| `crud/optiongroup.go` | `MutationOptions` — what a filtered write reads, and what it refuses |
| `crud/batch.go` | exact optional-verb dispatch and fail-closed error |

## Tests that walk this flow

- `TestSaveRefusesToWriteIntoAnotherTenant` — `crud/decorators/security/security_test.go`.
- `TestSaveOfAnotherTenantsAssignedKeyLooksMissing` — `crud/decorators/security/gate_edge_test.go` — the unscoped probe.
- `TestSaveRefusesToOverwriteATombstoneHiddenByRepositoryScope` — `crud/decorators/security/gate_edge_test.go` — the same probe over a soft-deleted row.
- `TestScopedSaveKeepsAConcurrentCreateCreateOnly` — `crud/decorators/security/security_test.go` — the other branch of the probe.
- `TestASaveCarryingAHiddenRowsKeyIsRefusedInsteadOfPassingAsACreate` — `crud/decorators/security/tombstone_test.go` — the probe reads past the soft-delete rule, and the action never becomes `Create`.
- `TestScopedSavePinsAnUpdateToItsInspectedSnapshot` — `crud/decorators/security/security_test.go` — the snapshot in the scoped upsert's `WHERE`, which is what closes the probe-to-write window.
- `TestScopedSaveCarriesRelationScopesIntoItsFinalUpdate` — `crud/decorators/security/relscope_test.go` — the relation scopes travel with it.
- `TestScopedSaveCannotBypassAnInnerSecurityGate` — `crud/decorators/security/security_test.go` — the optional verb is not a way around a gate underneath.
- `TestSaveWithoutAPrincipalWritesNothing` — `crud/decorators/security/edge_test.go`.
- `TestSaveJudgesAFrozenFieldByItsValue` — `crud/decorators/security/edge_test.go`.
- `TestAFrozenFieldIsRefusedOnUpdateEvenWhenTheValueIsUnchanged` — `crud/decorators/security/edge_test.go` — the asymmetry.
- `TestFreezeRefusesAnUpdateThatNamesAFrozenField` — `crud/decorators/security/gate_edge_test.go`.
- `TestTheGateScopeIsInTheUpdatesOwnWhereClause` — `crud/decorators/security/gate_edge_test.go`.
- `TestAnUpdateOfARowThatLeftTheScopeIsNotFound` — `crud/decorators/security/gate_edge_test.go`.
- `TestUpdateIsScopedAndFreezesTheScopeField` — `crud/decorators/security/security_test.go`.
- `TestAGatedFilteredWriteRefusesPagingRatherThanWritingEveryRowItShowedTheRule` — `crud/decorators/security/updateall_test.go` — the caller's `Limit` on a gated `UpdateAll` and `DeleteAll`, with a control that the same write without it goes through and `Inspect` saw both rows.
- `TestDeleteIsScoped` — `crud/decorators/security/security_test.go` — Delete re-expressed as DeleteAll.
- `TestDeleteChunksAfterChargingScopeAndSoftDeleteBinds` —
  `crud/sqlrepo/bind_budget_test.go` — declaration scope, tombstone and ids all
  share the budget, and every chunk keeps them.
- `TestUnscopedDeleteAllIsRefused` — `crud/decorators/security/security_test.go`.
- `TestUpdateAllIsScopedInTheStatementItself` — `crud/decorators/security/updateall_test.go`.
- `TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllRefusesAFrozenField` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllInspectsEveryRowItIsAboutToWrite` — `crud/decorators/security/updateall_test.go`.
- `TestUpdateAllIsRefusedByAReadOnlyPolicy` — `crud/decorators/security/updateall_test.go`.
- `TestInspectAbortsTheWholeCall` — `crud/decorators/security/edge_test.go`.
- `TestGateTreatsEveryInsertBatchRowAsCreateBeforeNativeIO` and
  `TestPortableBatchSurvivesEveryBuiltInDecoratorOrder` —
  `crud/sqlrepo/insert_batch_test.go`.
- `TestARowHiddenFromReadsIsStillDeletableByID` — `crud/http/crudfiber/write_edge_test.go` — what happens *without* a gate, and why `WithScope` is not one.

## See also

[[FL-007]] [[FL-002]] [[FL-003]] [[FL-011]]
