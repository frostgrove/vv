# D-031 — Soft delete belongs where the statements are built

**Status:** accepted
**Invariant:** Declaring a soft delete declares both halves — the stamp and the read filter — in one place.

## The decision

`basic.SoftDelete("DeletedAt")` is a blueprint setting, not a decorator. It folds
`IsNull(field)` into the permanent scope and rewrites `Delete` / `DeleteAll` into
one `UPDATE` that stamps the column, under exactly the narrowing the `DELETE`
would have had.

## Why

It was written as a decorator first, and the decorator could not work. A
middleware sits above `crud.Core`, and `Core` has no verb for "write this
column": `Update` takes the DTO declared at `Define` time, and `UpdatePlan`
refuses any other type. Synthesising a DTO would have meant either putting the
tombstone into the caller's update DTO — where a client could `PATCH` it — or
adding a raw-column verb to the seam, which is a much larger change than the
feature is worth.

The stamp is a statement. It belongs with the statements.

**Why one setting rather than two.** The hand-written recipe is `basic.Scope` for
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
- Do not promise that a unique index tolerates tombstones. It does not, and the
  fix is a partial index this library cannot write.

## Where it lives

- `repo/basic/blueprint.go:SoftDelete`
- `repo/basic/blueprint.go:resolveSoftDelete` — the validation and the scope fold.
- `repo/basic/repository.go:stamp` — the UPDATE both deletes become.
- `crud/meta.go:NowFunc` — the clock.

## Proven by

- `TestASoftDeleteStampsRatherThanRemoves` in `test/integration/softdelete_test.go`
  — gone from the repository, still in the table.
- `TestWithoutTheSettingADeleteStillRemovesTheRow` in the same file — the control
  that makes the rest mean something.
- `TestATombstonedRowCannotBeUpdated`, `TestDeletingATombstoneAgainChangesNothing`,
  `TestASoftDeleteAllHonoursTheFilter`, `TestABadSoftDeleteDeclarationIsRefused`.

## See also

[[D-004]] [[D-007]] [[D-011]]
