# D-025 — `crud.mapKey` collapses non-comparable relation keys

**Status:** open
**Invariant:** Two ends of a relation must agree on the map key derived from a parent key and a child foreign key, and two *different* keys must never derive the same map key.

## The decision

`mapKey` normalises a relation key so a parent's primary key and a child's
foreign key index the same bucket even when their Go types differ. Every signed
integer widens to `int64`, every unsigned integer that fits widens to `int64`,
every string kind flattens to `string`, and `[]byte` becomes `string` the way a
text driver hands it over.

## Why the normalisation exists at all

A parent's key and a child's foreign key are separate struct fields and need not
share a Go type. `int32(1)` and `int64(1)` are different map keys and the same
row. Without the widening, the children are filed under a key no parent looks
for, and the preload hands back empty lists with a 200 — no error anywhere, and
the shape of the bug is "this relation is always empty for some models".

The same argument covers `[]byte` → `string`: a text driver hands a `char` key
over as bytes on one path and as a string on another, in the same query.

## What is unresolved

The fallback:

```go
if !rv.Type().Comparable() {
    return rv.String()
}
```

`reflect.Value.String()` on a value whose kind is not `String` does not
serialise it. It returns `"<T Value>"` — a constant per type. So *every*
non-comparable key of a given type derives the same map key, and a preload over
such a relation gives every parent every child.

The failure is silent in the worst way: no error, a 200, and children attached
to the wrong parents.

**How reachable is it?** Not, today, from anything in this repository:

- A repository's primary key is the `ID comparable` type parameter, so it cannot
  be non-comparable.
- No model in `example/`, `test/` or the documentation has a relation key that is
  a slice, a map, or a struct containing one.
- The realistic shapes — `int64`, `uint`, `string`, `uuid.UUID` (a `[16]byte`
  array, which is comparable) — all take a branch above the fallback.

So this is a trap rather than a live bug. It becomes live the first time someone
declares a relation whose join column maps to a non-comparable Go type — a
custom `type Tags []string` with a `driver.Valuer`, or a JSON-backed composite
key.

**Why it has not been fixed.** Fixing it means choosing a serialisation, inside
`crud` — the package that may not import anything outside the standard library
([[D-016]]). The candidates all have a cost:

1. `fmt.Sprintf("%v", v)` — pulls in `fmt`, which `crud` already uses, so this is
   cheap. But `%v` is not injective: two distinct slices can print identically
   (`[]any{1, "2"}` and `[]any{"1", 2}` both print `[1 2]`). Trading a total
   collapse for an occasional one is better, not correct.
2. `driver.Valuer` first, then `%v` — better, because the key came from a column
   and a column's `Valuer` is the value the database actually stored. Still falls
   through to `%v` for anything without one, and calling a `Valuer` here means
   handling its error on a path that currently cannot fail.
3. `encoding/json` — injective enough for real keys, but it allocates per key on
   a hot path (once per parent, once per child) and it can fail.
4. **Refuse at declaration time.** `Relation.Resolve` could reject a relation
   whose local or remote field type is non-comparable and not a `[]byte`. That
   fits the library's eager-validation habit ([[D-021]]) and costs nothing at
   run time. It is probably the right answer, and it is a behaviour change for
   anyone who has such a relation and is getting away with it.

Option 4 plus keeping the current fallback for `[]byte` is the current leaning,
but it has not been decided.

## What it forbids

While this is open, do not:

- Remove the integer widening or the string flattening. Those are the part that
  works, and the bug they prevent is the same shape of silent wrong answer.
- Remove the `[]byte` → `string` conversion. A text driver hands a `char` key
  over as bytes on one path and a string on another.
- "Fix" the fallback with `fmt.Sprintf("%v", v)` without saying in the code that
  it is not injective.
- Add a dependency to `crud` to solve it. See [[D-016]].

## Where it lives

- `crud/preload.go:mapKey` — the whole function, with the comment explaining the
  part that works.
- `crud/preload.go:preloader.load` — parent-key dedup and the child index; both
  go through `mapKey`.
- `crud/preload.go:preloader.fetch` — derives the owner key, either from the join
  table's owner column (read as the *parent's* type, because `int32(1)` does not
  equal `int64(1)`) or from the child's foreign key.
- `crud/access.go:ElemValue` — unwraps an `Opt` or a pointer before `mapKey` sees
  it.
- `crud/relation.go:Relation.Resolve` — where a declaration-time refusal would go.

## Proven by

The part that works is tested; the unresolved part is not, and that is the gap.

- `TestPreloadMatchesKeysOfDifferentWidths` in `crud/preload_test.go` — the
  integer widening.
- `TestPreloadManyToManyReadsTheOwnerKeyAsTheOwnersType` in
  `crud/preload_test.go` — the join-table half.
- `TestPreloadManyToManyCarriesTheOwnerKey` in `crud/preload_test.go`.
- `TestANullableUUIDColumnRoundTrips` in `test/integration/uuid_test.go` — a
  comparable array key through a relation.

There is **no test for the non-comparable fallback**, because there is no model
in the tree that reaches it. Writing one would be the first step toward settling
this.

## See also

[[D-006]] [[D-016]] [[D-021]]
