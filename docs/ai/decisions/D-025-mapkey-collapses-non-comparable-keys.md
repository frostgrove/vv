# D-025 — `crud.mapKey` collapses non-comparable relation keys

**Status:** accepted
**Invariant:** Two ends of a relation must agree on the map key derived from a parent key and a child foreign key, and two *different* keys must never derive the same map key.

## The decision

The preloader normalises a relation key so a parent's primary key and a child's
foreign key index the same bucket even when their Go types differ. Every signed
integer widens to `int64`, every unsigned integer that fits widens to `int64`,
every string kind flattens to `string`, and `[]byte` becomes `string` the way a
text driver hands it over. Named byte slices use the same rule. A custom
`driver.Valuer` is canonicalised through the exact scalar/bytes value it gives
the SQL driver; that resolved value is also what the preload binds. The Valuer
is called once per observed key and byte values are copied, so a stateful or
buffer-reusing implementation cannot split those two identities.

The accepted Valuer result is deliberately narrower than Go 1.26's
`driver.IsValue`: nil, `[]byte`, bool, float64, int64, string or `time.Time`.
The structural decimal `Decompose` protocol may return a mutable pointer and is
not understood by the direct pgx adapter, so it is refused rather than indexed
by pointer identity or replaced with a database/sql-only wrapper.

## Why the normalisation exists at all

A parent's key and a child's foreign key are separate struct fields and need not
share a Go type. `int32(1)` and `int64(1)` are different map keys and the same
row. Without the widening, the children are filed under a key no parent looks
for, and the preload hands back empty lists with a 200 — no error anywhere, and
the shape of the bug is "this relation is always empty for some models".

The same argument covers `[]byte` → `string`: a text driver hands a `char` key
over as bytes on one path and as a string on another, in the same query.

## The failure that was closed

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

**How reachable was it before the regression fixtures?** No production/example
model in the repository used this shape:

- A repository's primary key is the `ID comparable` type parameter, so it cannot
  be non-comparable.
- No model in `example/`, the integration suite or the documentation had a
  relation key that was a slice, a map, or a struct containing one.
- The realistic shapes — `int64`, `uint`, `string`, `uuid.UUID` (a `[16]byte`
  array, which is comparable) — all take a branch above the fallback.

So this is a trap rather than a live bug. It becomes live the first time someone
declares a relation whose join column maps to a non-comparable Go type — a
custom `type Tags []string` with a `driver.Valuer`, or a JSON-backed composite
key.

Serialising an arbitrary Go value is not a sound fix inside `crud` — the package
may not import anything outside the standard library ([[D-016]]), and neither
`fmt` nor JSON proves that two database keys have different encodings. The
rejected candidates were:

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
4. **Refuse unsupported declarations before SQL.** `Relation.Resolve` validates
   both resolved join fields, and `Meta.ValidateRelationPath` performs the same
   non-publishing validation for declarative `RelationScope` setup. A
   non-comparable type is accepted only when it is a byte slice or implements
   `driver.Valuer`; every other declaration gets an actionable `SchemaError`.
   An interface or statically comparable struct/array may still carry a dynamic
   slice/map, so each actual value is additionally checked with
   `reflect.Value.Comparable()` before map access. An invalid parent value fails
   before SQL; an invalid scanned child fails before relation assignment.

Option 4 is the decision. It keeps the established integer/string/byte
normalisation and allows a database-native custom scalar without inventing a
second serialization vocabulary. Dynamic value-shape failures, `driver.Valuer`
errors, unsupported return types and panics stay internal runtime errors, never
client-facing `SchemaError`s and never process panics.

## What it forbids

Do not:

- Remove the integer widening or the string flattening. Those are the part that
  works, and the bug they prevent is the same shape of silent wrong answer.
- Remove the `[]byte` → `string` conversion. A text driver hands a `char` key
  over as bytes on one path and a string on another.
- "Fix" the fallback with `fmt.Sprintf("%v", v)` without saying in the code that
  it is not injective.
- Add a dependency to `crud` to solve it. See [[D-016]].

## Where it lives

- `crud/preload.go:canonicalPreloadKey` — one immutable observation produces
  both the driver bind and canonical map identity; dynamic values are checked
  here.
- `crud/preload.go:preloadMapKey` — the child-side map-key wrapper.
- `crud/preload.go:preloader.load` — parent-key canonicalisation, dedup and the
  child index.
- `crud/preload.go:preloader.fetch` — derives the owner key, either from the join
  table's owner column (read as the *parent's* type, because `int32(1)` does not
  equal `int64(1)`) or from the child's foreign key.
- `crud/access.go:relationKeyValue` — unwraps explicit `Opt` state while
  preserving pointers until Valuer precedence has been decided.
- `crud/relation.go:Relation.Resolve` and `Meta.ValidateRelationPath` — validate
  the two declarations at runtime resolution and non-publishing declarative
  setup respectively.

## Proven by

- `TestPreloadMatchesKeysOfDifferentWidths` in `crud/preload_test.go` — the
  integer widening.
- `TestPreloadManyToManyReadsTheOwnerKeyAsTheOwnersType` in
  `crud/preload_test.go` — the join-table half.
- `TestPreloadManyToManyCarriesTheOwnerKey` in `crud/preload_test.go`.
- `TestANullableUUIDColumnRoundTrips` in `test/integration/uuid_test.go` — a
  comparable array key through a relation.
- `TestPreloadKeepsDistinctNamedByteSliceKeysApart` and
  `TestPreloadCanonicalisesNonComparableDriverValuerKeys` — the two supported
  non-comparable representations never collapse.
- `TestPreloadKeepsANonNilEmptyByteKeyDistinctFromNull` — empty bytes remain an
  empty driver value rather than becoming SQL NULL.
- `TestPreloadSnapshotsPointerOnlyDriverValuerExactlyOnce` and the typed-nil
  Valuer tests — pointer-only, stateful, aliasing and typed-nil implementations
  retain database/sql-compatible meaning.
- `TestPreloadRefusesMutableDecimalDriverValuesBeforeSQL` — a mutable
  database/sql-only decimal value cannot reintroduce pointer-identity collapse
  or break the direct pgx adapter.
- `TestPreloadRefusesADeclaredNonComparableRelationKeyBeforeSQL` and
  `TestPreloadRefusesADynamicallyNonComparableKeyWithoutPanicking` — static and
  dynamic unsupported shapes fail at their correct trust boundary.
- `TestRelationScopeRefusesUnsupportedJoinKeysDuringTryDefine` in
  `crud/sqlrepo/blueprint_edge_test.go` — declarative setup rejects the same
  invalid mapping without publishing relation metadata.

## See also

[[D-006]] [[D-016]] [[D-021]]
