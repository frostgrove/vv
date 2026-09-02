# D-026 — `gate.DeleteAll` fetches its victims with the caller's own options

**Status:** accepted — settled by [[D-087]]
**Invariant:** Every row a gated `DeleteAll` or `UpdateAll` writes must have been shown to `Policy.Inspect` first.

## How it was settled

Both halves of the hole are closed, and the analysis below is kept because it is
the argument for the shape of the fix rather than a description of the code.

1. **The victim fetch no longer carries the caller's paging.** `gate.UpdateAll`
   and `gate.DeleteAll` append `inspectionRead()`, which zeroes `Limit`, `Page`,
   `Offset` and both cursors before the `GetAll`. `Inspect` therefore sees every
   row the filter matches, not the first page of them. The divergence described
   below — ten inspected, all written — cannot happen in that form any more.
2. **The repository refuses the option instead of dropping it.** This is way out
   3, the one this document called the most defensible and had not taken:
   `repository.UpdateAll`, `repository.DeleteAll` and `repository.Update` now
   build their options through `crud.MutationOptions`, and a `Limit`, `Page`,
   `Offset`, cursor, sort, projection or preload is a `*crud.SchemaError` before
   any statement is planned. [[D-087]] is the general rule and its table.

The second, smaller question below — the unscoped-write guard and `crud.True()`
— is unchanged.

## The hole, as it was

`gate.DeleteAll` builds `scoped` = policy scope + relation narrowing + **the
caller's options**. When `Policy.Inspect` was set it once
fetched its victims with that list unaltered, inspected each row, and called
`g.Core.DeleteAll(ctx, scoped...)`.

The `DELETE` ignores `Limit`, `Page` and `Offset` — `repository.DeleteAll` builds
`DELETE FROM t WHERE …` and never emits a `LIMIT`. The `GetAll` did not:
`repository.GetAll` returns every matching row *only* when the options carry no
paging at all, and otherwise resolves a limit and an offset.

So a caller who passed `crud.Limit(10)` to a gated `DeleteAll` got `Inspect`
called on ten rows and the whole matching set deleted. `gate.UpdateAll` had the
same shape.

`gate.Delete` (by id) does **not** have the problem: it builds its own option
list (`crud.Where(within)`, plus the narrowing) and never forwards caller
options, because `Delete` takes ids and nothing else.

## Why the options are there at all

Passing the caller's options to the victim fetch is not gratuitous — it is what
makes the fetch return *the rows the delete will touch*. The caller's `Where`
clauses have to be in there or `Inspect` would be shown rows the statement will
not delete, and the policy would deny a delete over a row that was never at risk.

The bug is that a `[]crud.Option` cannot distinguish "narrow the set" from
"shape the response". `Where` is the first kind and `Limit`/`Page`/`Offset` are
the second, and the gate forwards both because it forwards the slice.

## The three ways out, and the two that were taken

1. **Strip paging from the victim fetch.** Append `crud.Unpaged()` — or, better,
   an option that zeroes `Limit`, `Page` and `Offset` — before the `GetAll`.
   Correct, but `Unpaged()` is still clamped by the blueprint's `MaxLimit`
   (`Options.Resolved`), so on a repository with a `MaxLimit` the hole narrows
   rather than closing. And on a large match set the fetch becomes unbounded,
   which is a memory question the gate currently does not have.
2. **Refuse paging options on a gated `DeleteAll`/`UpdateAll` when `Inspect` is
   set.** Honest, cheap, and a behaviour change for a caller who is passing a
   `Limit` today and getting away with it.
3. **Refuse paging options on `DeleteAll`/`UpdateAll` in the SQL repository.**
   They already do nothing there, so the option is meaningless on those two
   methods regardless of the gate. This is the widest fix and the one that
   removes the ambiguity rather than working around it. It also changes an API
   that currently silently ignores the option.

1 was taken in the gate, with the sharper option this list asked for:
`inspectionRead()` zeroes the paging and both cursors rather than setting
`Unpaged()`, so the `MaxLimit` clamp never applies. 3 was then taken in the
repository — a `Limit` on a filtered write has no meaning in any layer — and it
is the behaviour change that removes the ambiguity instead of working around it
([[D-087]]).

There is a second, smaller question in the same code: the unscoped-write guard
reads `crud.Build(opts...).Predicate() == nil`, so *any* caller predicate counts
as "scoped enough" even if it is `crud.True()`. That is a much weaker hole than
the one above and is arguably correct — a caller who writes `Where(True())` has
said what they mean.

## What it forbids

- Remove the caller's options from the victim fetch entirely. The `Where`
  clauses have to be there or `Inspect` sees the wrong rows.
- Make `repository.DeleteAll` or `repository.UpdateAll` honour a `Limit` as a
  way of "making the two agree". A partially applied filtered delete is worse
  than either current behaviour, and no dialect spells `DELETE … LIMIT`
  portably.
- Reintroduce the caller's paging into the victim fetch. `inspectionRead` is
  what makes "every row a gated filtered write touched was inspected" true, and
  it is not a formatting detail of that option list.

## Where it lives

- `crud/decorators/security/security.go:gate.DeleteAll` — the victim fetch and
  the unlimited `DELETE`.
- `crud/decorators/security/security.go:gate.UpdateAll` — the same shape.
- `crud/decorators/security/security.go:gate.Delete` — the id form, which does
  not forward caller options and is therefore unaffected.
- `crud/decorators/security/security.go:gate.scoped` — builds the option list
  that carries the caller's paging through.
- `crud/sqlrepo/repository.go:repository.GetAll` — returns everything only when no
  paging option is present; this is the asymmetry.
- `crud/sqlrepo/repository.go:repository.DeleteAll` /
  `crud/sqlrepo/repository.go:repository.UpdateAll` — no `LIMIT`, ever.
- `crud/options.go:Options.Resolved` — why `Unpaged()` alone would not fully
  close it.

## Proven by

- `TestUpdateAllInspectsEveryRowItIsAboutToWrite` in
  `crud/decorators/security/updateall_test.go` — asserts the intended invariant
  for the no-paging case.
- `TestInspectAbortsTheWholeCall` in `crud/decorators/security/edge_test.go`.
- `TestUnscopedDeleteAllIsRefused` in
  `crud/decorators/security/security_test.go` and
  `TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt` in
  `crud/decorators/security/updateall_test.go`.
- `TestGetAllIsNotCappedByMaxLimit` in `crud/sqlrepo/paging_edge_test.go` — the
  neighbouring decision that `GetAll` without paging options really does return
  everything, which is what the decorators that read a whole set in order to
  check it depend on.

- `TestAGatedFilteredWriteRefusesPagingRatherThanWritingEveryRowItShowedTheRule`
  in `crud/decorators/security/updateall_test.go` — the test this document asked
  for. A gated `UpdateAll` and a gated `DeleteAll` carrying `crud.Limit(1)` are
  refused with no `UPDATE` and no `DELETE` reaching the recorder; the control
  runs the same write without the option and asserts `Inspect` saw both rows and
  both were written.
- `TestAFilteredWriteRefusesTheOptionsItWouldNotApply` in
  `crud/sqlrepo/optiongroup_test.go` — the ungated half, which is where the
  refusal actually lives.

## See also

[[D-087]] [[D-008]] [[D-004]] [[D-010]]
