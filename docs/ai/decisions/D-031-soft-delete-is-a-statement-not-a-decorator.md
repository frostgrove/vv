# D-031 — Soft delete belongs where the statements are built

**Status:** accepted
**Invariant:** Declaring a soft delete declares both halves — the stamp and the read filter — in one place.

## The decision

`db:",serverowned,tombstone"` is the default lifecycle declaration. Codegen
removes that field from every wire write shape and emits a soft-deleting
blueprint; runtime metadata removes it from generic Save/Update plans. The
blueprint folds `IsNull(field)` into permanent live scope and rewrites `Delete` /
`DeleteAll` into an `UPDATE` that stamps the column under exactly the narrowing
the `DELETE` would have had. `Restore` is a separate lifecycle action that sets
the column to NULL only on tombstones. Both mutations advance an optimistic-lock
version when the model has one, so Delete→Restore cannot recreate an old
version/state pair. The lifecycle field must carry a nullable timestamp; an
incompatible nullable declaration is rejected at blueprint construction.

`sqlrepo.SoftDelete("DeletedAt")` remains the explicit low-level declaration for
models a consumer cannot tag. Its blueprint-local Schema view freezes the same
generic write paths without mutating a raw blueprint over the same Go model.

## Why

It was written as a decorator first, and the decorator could not work. A
middleware sits above `crud.Core`, while the lifecycle column and its permanent
scope are storage concerns. Putting the tombstone into the caller's update DTO
lets a client `PATCH` it under Update permission. The dedicated optional
Restore capability instead crosses decorators only when each one explicitly
forwards it; security authorises, scopes and snapshots it as `Restore`.

The stamp is a statement. It belongs with the statements.

**Why one setting rather than two.** The hand-written recipe is `sqlrepo.Scope` for
the reads plus a service layer overriding two methods for the writes. Adding the
first and forgetting the second fails silently in the worst direction: the reads
hide rows the deletes are still destroying. One declaration cannot be half
applied.

**Why the count is what it removed from view.** The scope already carries "not
deleted", so a row stamped twice is stamped once. `Delete` on a tombstone returns
0, which is the same answer a real delete gives for a row that is already gone.

**Why nullable is required.** "Not deleted" needs a value. A non-nullable column
has none, so every row would read as a tombstone the moment the scope is added.

## What it forbids

- Do not add the read scope and the delete rewrite as separate declarations.
- Do not stamp outside the scope. A soft delete that reached rows a scope was
  hiding would be a delete that reached them.
- Do not expose a tombstone through generic PATCH/create/replace or full Save.
- Do not authorise Restore as Update; making a hidden row visible is a distinct
  lifecycle decision.
- Do not promise that a unique index tolerates tombstones. It does not, and the
  fix is a partial index this library cannot write.

## Where it lives

- `crud/sqlrepo/blueprint.go:SoftDelete`
- `crud/sqlrepo/blueprint.go:resolveSoftDelete` — the validation and the scope fold.
- `crud/sqlrepo/repository.go:stamp` — the UPDATE both deletes become.
- `crud/lifecycle.go`, `crud/sqlrepo/repository.go:Restore` — explicit restore.
- `crud/decorators/security/security.go:Restore` — action, scope and snapshot.
- `internal/codegen` — generated wire exclusion and blueprint declaration.
- `crud/meta.go:NowFunc` — the clock.

## Proven by

- `TestASoftDeleteStampsRatherThanRemoves` in `test/integration/softdelete_test.go`
  — gone from the repository, still in the table.
- `TestWithoutTheSettingADeleteStillRemovesTheRow` in the same file — the control
  that makes the rest mean something.
- `TestATombstonedRowCannotBeUpdated`, `TestDeletingATombstoneAgainChangesNothing`,
  `TestASoftDeleteAllHonoursTheFilter`, `TestABadSoftDeleteDeclarationIsRefused`.
- `TestTaggedTombstoneMakesSoftDeleteTheDeclarativeDefault`,
  `TestLegacySoftDeleteSettingStillFreezesGenericWrites`, and
  `TestRestoreHasItsOwnAuthorizationAndScope`.

## See also

[[D-004]] [[D-007]] [[D-011]]
