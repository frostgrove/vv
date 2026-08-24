# D-001 — Two-parameter decoratable seam, three-parameter typed façade

**Status:** accepted
**Invariant:** A middleware must be declarable with two type parameters `[M, ID]` and must be usable at a call site that writes no explicit generics.

## The decision

`crud.Core[M, ID]` is the interface every decorator implements. Its `Update`
takes `dto any`. `crud.Repo[M, ID, U]` is a struct that embeds a `Core` and
shadows `Update` and `UpdateAll` with versions typed against the update DTO `U`.
Consumers hold a `Repo`; decorators only ever see a `Core`.

## Why

The alternative is one three-parameter interface. It costs inference. Go infers
type parameters from a function's arguments, and a middleware's arguments name
the model and the id — a `security.Policy[M, ID]`, an audit sink over `*M` —
never the update DTO. With `U` on the decorated interface, `security.Gate(policy)`
would not compile without spelling all three parameters at every `Bind` call,
and the same for every third-party decorator anyone writes.

Erasing `U` to `any` on the seam moves the cost to one place: `UpdatePlan`
already resolves DTO fields reflectively, so it has to check the dynamic type
anyway (`crud/update.go:UpdatePlan.dtoValue`). The façade puts the compile-time
check back at the only boundary a consumer touches.

## What it forbids

- Do not add `U` to `Core`. It re-breaks inference for every middleware.
- Do not turn `Repo` into an interface. It is a struct precisely so that the
  embedded `Core` promotes every method it does not shadow; an interface would
  have to redeclare all of them, and a new `Core` method would then be a
  breaking change for every implementor rather than a free promotion.
- Do not make `Middleware` take three parameters "for symmetry".

## Where it lives

- `crud/repo.go:Core` — the two-parameter seam; `Update(ctx, id, dto any, ...)`.
- `crud/repo.go:Repo` — the façade; embeds `Core`, shadows `Update`/`UpdateAll`
  with `dto U`.
- `crud/repo.go:Middleware` — `func(Core[M, ID]) Core[M, ID]`.
- `crud/repo.go:Chain` / `crud/repo.go:Decorate` — `mw[0]` ends up outermost.
- `crud/repo.go:Base` — embeddable pass-through, for a decorator that overrides
  two methods out of eleven.
- `repo/basic/blueprint.go:Blueprint.Bind` — `Bind(src, mw...)` wraps the
  repository, then `crud.Wrap` re-types it.
- `repo/decorators/security/security.go:Gate` — `Gate[M, ID](p Policy[M, ID])`,
  the case the decision exists for.

## Proven by

- `TestGateComposesWithOtherMiddleware` in
  `repo/decorators/security/security_test.go` — a gate stacked with a second
  middleware, both declared without explicit generics.
- `TestDecorateStacksWithTheFirstMiddlewareOutermost` in `crud/decorate_test.go`
  — would catch a reversed chain, which silently moves a security layer inside
  the thing it is supposed to guard.
- `TestBasePassesEverythingThroughAndLetsOneOverrideWin` in
  `crud/decorate_test.go` — would catch a `Base` that stopped promoting a
  method, so an override of one method dropped the other ten.
- `TestUnwrapReturnsTheDecoratedCore` in `crud/decorate_test.go`.

## See also

[[D-022]] [[D-021]] [[D-008]]
