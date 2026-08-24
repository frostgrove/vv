# D-026 — `gate.DeleteAll` fetches its victims with the caller's own options

**Status:** open
**Invariant:** Every row a gated `DeleteAll` or `UpdateAll` writes must have been shown to `Policy.Inspect` first.

## The decision — and where it does not hold

`gate.DeleteAll` builds `scoped` = policy scope + relation narrowing + **the
caller's options**, then, when `Policy.Inspect` is set, runs

```go
victims, err := g.Core.GetAll(ctx, g.whole(true, scoped)...)
```

inspects each row, and finally calls `g.Core.DeleteAll(ctx, scoped...)`.

The `DELETE` ignores `Limit`, `Page` and `Offset` — `repository.DeleteAll` builds
`DELETE FROM t WHERE …` and never emits a `LIMIT`. The `GetAll` does not:
`repository.GetAll` returns every matching row *only* when the options carry no
paging at all, and otherwise resolves a limit and an offset.

So a caller who passes `crud.Limit(10)` to a gated `DeleteAll` gets `Inspect`
called on ten rows and the whole matching set deleted.

`gate.UpdateAll` has the same shape at
`repo/decorators/security/security.go:gate.UpdateAll` — the target fetch uses
`scoped`, and the `UPDATE` is unlimited.

`gate.Delete` (by id) does **not** have the problem: it builds its own option
list (`crud.Where(within)`, plus the narrowing) and never forwards caller
options, because `Delete` takes ids and nothing else.

## Why it is like this

Passing the caller's options to the victim fetch is not gratuitous — it is what
makes the fetch return *the rows the delete will touch*. The caller's `Where`
clauses have to be in there or `Inspect` would be shown rows the statement will
not delete, and the policy would deny a delete over a row that was never at risk.

The bug is that a `[]crud.Option` cannot distinguish "narrow the set" from
"shape the response". `Where` is the first kind and `Limit`/`Page`/`Offset` are
the second, and the gate forwards both because it forwards the slice.

## What is unresolved

Which of these to do:

1. **Strip paging from the victim fetch.** Append `crud.Unpaged()` — or, better,
   an option that zeroes `Limit`, `Page` and `Offset` — before the `GetAll`.
   Correct, but `Unpaged()` is still clamped by the blueprint's `MaxLimit`
   (`Options.Resolved`), so on a repository with a `MaxLimit` the hole narrows
   rather than closing. And on a large match set the fetch becomes unbounded,
   which is a memory question the gate currently does not have.
2. **Refuse paging options on a gated `DeleteAll`/`UpdateAll` when `Inspect` is
   set.** Honest, cheap, and a behaviour change for a caller who is passing a
   `Limit` today and getting away with it.
3. **Refuse paging options on `DeleteAll`/`UpdateAll` in the *basic* repository.**
   They already do nothing there, so the option is meaningless on those two
   methods regardless of the gate. This is the widest fix and the one that
   removes the ambiguity rather than working around it. It also changes an API
   that currently silently ignores the option.

Option 3 is the most defensible — a `Limit` on a filtered write has no meaning
in any layer — but it is the biggest behaviour change and it has not been made.

There is a second, smaller question in the same code: the unscoped-write guard
reads `crud.Build(opts...).Predicate() == nil`, so *any* caller predicate counts
as "scoped enough" even if it is `crud.True()`. That is a much weaker hole than
the one above and is arguably correct — a caller who writes `Where(True())` has
said what they mean.

## What it forbids

While this is open, do not:

- Remove the caller's options from the victim fetch entirely. The `Where`
  clauses have to be there or `Inspect` sees the wrong rows.
- Make `repository.DeleteAll` or `repository.UpdateAll` honour a `Limit` as a
  way of "making the two agree". A partially applied filtered delete is worse
  than either current behaviour, and no dialect spells `DELETE … LIMIT`
  portably.
- Assume `Inspect` has seen every row a gated filtered write touched. Until this
  is settled, that is exactly the assumption that does not hold.

## Where it lives

- `repo/decorators/security/security.go:gate.DeleteAll` — the victim fetch and
  the unlimited `DELETE`.
- `repo/decorators/security/security.go:gate.UpdateAll` — the same shape.
- `repo/decorators/security/security.go:gate.Delete` — the id form, which does
  not forward caller options and is therefore unaffected.
- `repo/decorators/security/security.go:gate.scoped` — builds the option list
  that carries the caller's paging through.
- `repo/basic/repository.go:repository.GetAll` — returns everything only when no
  paging option is present; this is the asymmetry.
- `repo/basic/repository.go:repository.DeleteAll` /
  `repo/basic/repository.go:repository.UpdateAll` — no `LIMIT`, ever.
- `crud/options.go:Options.Resolved` — why `Unpaged()` alone would not fully
  close it.

## Proven by

The surrounding behaviour is tested; the hole itself is not.

- `TestUpdateAllInspectsEveryRowItIsAboutToWrite` in
  `repo/decorators/security/updateall_test.go` — asserts the intended invariant
  for the no-paging case.
- `TestInspectAbortsTheWholeCall` in `repo/decorators/security/edge_test.go`.
- `TestUnscopedDeleteAllIsRefused` in
  `repo/decorators/security/security_test.go` and
  `TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt` in
  `repo/decorators/security/updateall_test.go`.
- `TestGetAllIsNotCappedByMaxLimit` in `repo/basic/paging_edge_test.go` — the
  neighbouring decision that `GetAll` without paging options really does return
  everything, which is what the decorators that read a whole set in order to
  check it depend on.

**No test covers a gated `DeleteAll` or `UpdateAll` with a caller-supplied
`Limit`.** Writing one — asserting that `Inspect` saw as many rows as the write
touched — is the first step toward settling this, and it will fail today.

## See also

[[D-008]] [[D-004]] [[D-010]]
