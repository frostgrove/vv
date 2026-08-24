# `catalog` — not implemented

**Tier:** implementation, root module. It costs a consumer nothing.

**What it owes:** per-database schema introspection — tables, columns, their
nullability and collation, the primary key, unique constraints and unique
indexes separately, foreign keys in both directions, and check constraints.

**Open before any code:**

- Keyed on the identity `crud.Identified.DataSource()` reports, but the test is
  `keyOf(src) != nil`, **not** `src.(Identified)`. A `crud.ReadWrite` pair *is*
  `Identified` and answers nil when its primary is not, so the interface test
  collapses every such source into one entry.
- Not a `map[any]`. `crud.sameDataSource` avoids one deliberately, because a
  datasource handle need not be comparable and an uncomparable map key panics.
- Lookups take no `context`: a loaded catalog does no I/O. `Load` is the thing
  that fails, and it fails at start-up (D-021). `Constraint` takes the table as
  well as the name — MySQL calls every InnoDB primary index `PRIMARY`.

**Governed by:** [ROADMAP-errors.md](../ROADMAP-errors.md) §7, and its phase 6.
