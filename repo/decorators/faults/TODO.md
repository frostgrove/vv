# `repo/decorators/faults` — not implemented

**Tier:** implementation, root module. A decorator over `crud.Core`, in the same
family as `repo/decorators/security` and `repo/decorators/specs`.

**What it owes:** turning an integrity error into a rich `errs.Fault` — the
column-to-model-field hop of the path chain, and the place `probe` is invoked
from.

**What phase 3 already hands it.** The adapters classify: a refused statement
comes back as an `*errs.Fault` carrying a `Code`, a `Kind`, one `Violation`
marked `OriginState`, and a `Source` holding whatever the driver named — the
constraint and the table on PostgreSQL, and the columns too where a catalog was
wired. What is left blank, deliberately, is `Violation.Path`, `Fault.Op`,
`Fault.Entity`, `Detail.Value`, `Detail.RefTable` and `Detail.RefColumns`. So
this decorator's job is the column→model-field hop plus `Op` and `Entity`, and
not the classification.

Setting `Op` is worth naming separately: it is what unblocks the foreign-key
direction [[D-046]] defers. On PostgreSQL and SQLite a missing parent and a child
still referring to a row are the same key with the same fields, and only the verb
separates them.

**Open before any code:**

- It resolves the model field through `crud.Meta`, not `crud.Schema`. `Schema` is
  table-independent and cached per type, so it cannot tell two databases' `users`
  apart.
- Decorator order decides whether the probe is scoped by `security.Policy`. The
  gate is a middleware *above* the repository; this sits below. Nothing states
  the order yet, and it is what decides whether the probe can see hidden rows.
- Where it gets the datasource, and therefore the catalog: `crud.Core` exposes
  `Meta()` and no source.

**Governed by:** [ROADMAP-errors.md](../../../ROADMAP-errors.md) §4, phase 4.
§14's table gives phase 3 the adapters and [[FL-014]] and gives "Render +
decorators" to phase 4; §3's hop table gives constraint→model-field to this
decorator *through `crud.Meta`*, which an adapter does not have.
