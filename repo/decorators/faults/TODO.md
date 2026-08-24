# `repo/decorators/faults` — not implemented

**Tier:** implementation, root module. A decorator over `crud.Core`, in the same
family as `repo/decorators/security` and `repo/decorators/specs`.

**What it owes:** turning an integrity error into a rich `errs.Fault` — the
column-to-model-field hop of the path chain, and the place `probe` is invoked
from.

**Open before any code:**

- It resolves the model field through `crud.Meta`, not `crud.Schema`. `Schema` is
  table-independent and cached per type, so it cannot tell two databases' `users`
  apart.
- Decorator order decides whether the probe is scoped by `security.Policy`. The
  gate is a middleware *above* the repository; this sits below. Nothing states
  the order yet, and it is what decides whether the probe can see hidden rows.
- Where it gets the datasource, and therefore the catalog: `crud.Core` exposes
  `Meta()` and no source.

**Governed by:** [ROADMAP-errors.md](../ROADMAP-errors.md) §4, phase 3.
