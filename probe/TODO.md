# `probe` — not implemented

**Tier:** implementation, root module.

**What it owes:** `Simple` and `Full` violation handlers behind one interface —
the thing that turns a database's one-violation-at-a-time answer into every
violation the payload would cause.

**Open before any code:**

- Foreign-key terms need the NULL guard. A nullable FK left NULL satisfies the
  constraint, so a bare `NOT EXISTS(… WHERE id = NULL)` reports a violation on a
  field that is correct — measured against PostgreSQL 17. A probe may only ever
  **narrow** the truth.
- Values come from `merge(loaded row, changes)`, not from the change set.
  `crud.UpdatePlan` drops fields whose value already matches, so a composite
  unique constraint has no value to bind for its unchanged half.
- Results are read by column position. PostgreSQL truncates identifiers at 63
  bytes with a `NOTICE` no driver surfaces.
- Placeholders and quoting go through `crud.Dialect`. `$1` is PostgreSQL-only.
- A probe that errors keeps the driver's violation and sets `Partial: true`. It
  must never downgrade a correct 409 into an opaque 500.
- It has a unit-test seam and needs no change to `crud/crudtest`. Phase 6
  measured it: `crud.KeyOf` takes a source that cannot name its database at face
  value and returns the source itself, so a recorder keys as itself, two
  recorders are two catalogs, and the §16 question about growing a
  `DataSource()` is answered **no** ([[D-041]]). Adding one would rescope
  `crud.InTx` over every recorder in the tree.

**Governed by:** [ROADMAP-errors.md](../ROADMAP-errors.md) §8, and its phase 7.
