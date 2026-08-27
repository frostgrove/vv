# D-054 — The closed predicate AST gets one marshaller, inside `crud`, refusing three nodes by name

**Status:** accepted
**Invariant:** `crud.MarshalPredicate` is the only way a predicate leaves this process, `document` is on the `Predicate` interface so a new node cannot forget it, and `crud.Raw`, `crud.EqField` and `crud.False` are refused rather than approximated.

## The decision

`crud.Predicate` is a closed AST: `render` is unexported, every node type is
unexported, and nothing outside `crud` can implement or inspect one. That is
[[D-004]]'s guarantee — a security decorator can trust that whatever it ANDs in
cannot be peeled off — and it is why `remote` cannot serialise a filter itself.

So the marshaller lives in `crud`:

```go
func MarshalPredicate(p Predicate) (json.RawMessage, error)
```

Three things about its shape are decided rather than incidental.

**It is a method on the interface, not a type switch.** `Predicate` gained a
second unexported method, `document(*docWriter)`. A node added to `predicate.go`
without a document form does not compile.

**It returns `json.RawMessage`, not a `query.Filter`.** `query` imports `crud`,
so the dependency cannot run the other way. The caller wraps it with
`query.RawFilter`.

**Every node writes a single-key object, and composition is always an array.**
`crud.And(Gte("Views",1), Lte("Views",10))` becomes
`{"and":[{"Views":{"gte":1}},{"Views":{"lte":10}}]}` and never a merged object.

And three nodes are refused:

| node | constructor | why |
|---|---|---|
| `rawNode` | `crud.Raw` | it is SQL |
| `fieldCmpNode` | `crud.EqField` | the DSL compares a field to a value, never to a field |
| `constNode(false)` | `crud.False`, `crud.Or()` | it matches no rows, and no document says that |

## Why

**`crud.Raw` is refused because it is SQL, and that is a security answer rather
than a missing word.** The DSL carries field paths and values, and every path is
resolved against the model schema before any SQL is built ([[D-013]]). A raw
fragment has been through none of that. Putting one on a wire would mean a
service accepting SQL text from a caller, which is the shape of every injection
this design exists to make impossible. The refusal holds even for a fragment
with no user input in it, because the rule has to be readable at a glance.

**`crud.False` is refused because the failure is asymmetric.** `crud.True()`
narrows nothing, and an empty filter document narrows nothing, so True is
written as `{}`. Its opposite has no document at all — and the tempting
approximation, `{"not":{}}`, is read by the compiler as an empty inner object,
which drops the `not` with it. "No rows" would arrive as "every row": the exact
inversion, with a 200 on it. The same asymmetry decides two more arms — an `And`
drops an unconditional term and an `Or` swallowed by one becomes `{}` — and each
is written out rather than left to the far side to work out from a document with
an empty object in it.

**Merging is refused because JSON keeps the last of a repeated key.** Two
conditions on one field merged into one object need the field name twice, and
every decoder in every language keeps whichever copy came last. Half the
caller's filter would be gone, silently, and only for filters with two
conditions on one field — which is the ordinary shape of a range.

**The interface method rather than a type switch, because a switch has a
default.** A node added to `predicate.go` with no arm would fall through it, and
whatever the default did — drop the clause, or refuse everything — would be
wrong for one of the two directions. `go/ast` over the source was considered and
is strictly weaker: it is a test that can be skipped, where this is a build that
cannot.

**Why literal helpers carry their own wire operation.** `crud.Contains(f, s)`,
`StartsWith` and `EndsWith` retain their literal-helper mode until marshalling,
then use `contains`, `startsWith` or `endsWith` (and their `i…` variants). The
peer reconstructs the same mode, including wildcard quoting and its dialect's
`ESCAPE` clause. Raw `like` remains a raw SQL pattern by design. The round trip
is asserted on rendered SQL and binds rather than document spelling, because
that is what a caller can observe.

## What it forbids

- Do not add an exported way to inspect a `Predicate` from outside `crud`. This
  is the one, it is one-directional, and [[D-004]] rests on the AST staying
  closed.
- Do not put `crud.Raw` on a wire — not escaped, not parameterised, not behind a
  flag. If a caller needs an expression the DSL cannot say, the DSL is what
  should grow.
- Do not approximate `crud.False`, an empty `Or`, or `Not` of an unconditional
  predicate. Each means "no rows", and every document that looks close means
  "every row".
- Do not merge two conditions on one field into one object.
- Do not give `document` a default arm. The compiler is the check.
- Do not weaken the round-trip test to compare documents instead of SQL. The
  document is a shape; the SQL is the question.

## Where it lives

- `crud/document.go` — `MarshalPredicate`, `PredicateError`, `docWriter`, and one
  `document` method per node in `predicate.go`'s order.
- `crud/predicate.go:Predicate` — the interface, with the comment saying why the
  method is there rather than in a switch.
- `remote/options.go:requestOf` — the one caller, wrapping the result with
  `query.RawFilter`.

## Proven by

- `TestEveryFilterDocumentSurvivesARoundTripThroughAPredicate` in
  `crud/query/roundtrip_test.go` — thirty-two documents covering every operator in
  `crud/query/ops.go`, each compiled, marshalled, compiled again, and asserted to
  render byte-identical SQL with identical binds. The control is inside the
  loop: a case that renders no SQL fails outright, so the test cannot pass for a
  marshaller that produced `{}` and a compiler that read it as no filter.
  Verified by making `And` keep only its first term and watching four cases
  fail.
- `TestAPredicateTheWireCannotCarryIsRefusedByName` in the same file — the eight
  refusals, each asserted to be a `*crud.PredicateError` naming the constructor
  the call site wrote, including two nested inside an `And` and an `Or` so a
  refusal cannot be lost in a subtree. Verified by making `rawNode` write `{}`
  and watching both `crud.Raw` cases fail.
- `TestAnUnconditionalPredicateNarrowsNothingAndSwallowsAnOr` in the same file —
  the asymmetry, all four arms: `True()` and `And()` are `{}`, `And(True, x)`
  keeps `x`, and `Or(True, x)` is `{}` rather than `x`.
- `TestTwoConditionsOnOneFieldBothSurvive` in the same file — the repeated-key
  hazard, asserted twice over: the field name appears exactly twice in the
  document, and the re-compiled SQL and binds match the original.
- `TestAFilterWrittenInGoArrivesAsTheSameNarrowing` in
  `remote/roundtrip_test.go` and in `crud/rpc/crudgrpc/client_test.go` — the same
  claim end to end over both transports, asserted at the far side's repository
  rather than on the response, so a filter that never arrived fails.

## See also

[[D-053]] [[D-004]] [[D-013]] [[FL-018]] [[FL-012]]
