# D-002 — `Opt[T]` models three states, never two

**Status:** accepted
**Invariant:** For a nullable column, an absent JSON key, an explicit `null` and a value must produce three distinct outcomes: no SQL, `SET col = NULL`, and `SET col = v`.

## The decision

`crud.Opt[T]` is a struct carrying a value and a three-valued state:
`undefined`, `null`, `set`. It is the DTO field type for every nullable column
and a legal model field type for one. Its zero value is `undefined`, so a key
missing from a PATCH body decodes to the right state with no extra bookkeeping.
A `*T` is still used — but only for a *non-nullable* column, where two states
are the whole story.

## Why

A PATCH body has three intents for a nullable column and a pointer expresses
two. `nil` has to mean either "absent" or "set to NULL", and whichever you pick,
the other one is unreachable:

- `nil` means absent → a client can never null a column through PATCH.
- `nil` means NULL → every field the client omitted is written as NULL, which
  wipes the row on the first partial update.

The usual escape is `map[string]any` plus a hand-written switch per field. That
is the boilerplate the library exists to delete, and it is untyped, so a renamed
model field is a runtime surprise.

`UnmarshalJSON` is never called for an absent key, which is what makes the zero
value carry the meaning for free. `IsZero` exists so `json:",omitzero"` drops
undefined fields on the way back out.

## What it forbids

- Do not "simplify" a generated `crud.Opt[T]` DTO field to `*T`. The column is
  nullable; the third state disappears and PATCH loses either NULL-writing or
  absence.
- Do not give `Opt[T]` an exported field or a constructor that can produce
  `set` with a zero value where `null` was meant. `Set(v)` and `Null[T]()` are
  the only two producers, plus `FromPtr`, which deliberately never produces
  `undefined` — it is a converter from the two-state convention.
- Do not make `Scan` able to produce `undefined`. A row that came back from the
  database is by definition defined; a NULL column is `null`.
- The `optional` interface has unexported methods on purpose
  (`crud/opt.go:optional`). Nothing outside `crud` can impersonate an `Opt`, so
  `isOptType` cannot be fooled.

## Where it lives

- `crud/opt.go:optState` — the three constants.
- `crud/opt.go:Opt` — the type; `Set`, `Null`, `Undefined`, `FromPtr`.
- `crud/opt.go:Opt.UnmarshalJSON` — an explicit `null` becomes `null`, any other
  token becomes `set`; absence never reaches the method.
- `crud/opt.go:Opt.IsZero` — pairs with `json:",omitzero"`.
- `crud/opt.go:optional` — the non-generic view the reflective update planner
  reads without knowing `T`.
- `crud/meta.go:Field.comparableOf` — `undefined` and `null` both normalise to
  Go `nil` for diffing.
- `crud/update.go:planField.read` — where the DTO's three states turn into
  "write this / write NULL / write nothing".
- `cmd/vv/main.go:dtoType` — the generator picks `crud.Opt[T]` for a
  nullable column and `*T` for the rest.

## Proven by

- `TestOptUnmarshalsAbsentNullAndValueDifferently` in `crud/opt_edge_test.go` —
  the three JSON shapes.
- `TestOptStates` in `crud/opt_test.go`.
- `TestUpdateDistinguishesUndefinedFromNull` in
  `repo/basic/repository_test.go` — the end-to-end statement, which is where a
  regression would actually hurt.
- `TestUpdateCarriesAnExplicitNullThrough` in `http/crudfiber/handler_test.go`.
- `TestOnlyOmitzeroDropsAnUndefinedFieldOnMarshal` in `crud/opt_edge_test.go`.
- `TestFromPtrOfAZeroValueIsAValueNotAnAbsence` in `crud/opt_edge_test.go`.
- `TestGeneratedDTOTypesFollowNullability` in `example/blog/blog_test.go` — would
  catch the generator emitting `*T` for a nullable column.

## See also

[[D-010]] [[D-018]] [[D-011]]
