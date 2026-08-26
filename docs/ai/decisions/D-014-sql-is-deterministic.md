# D-014 — The same request produces byte-identical SQL

**Status:** accepted
**Invariant:** Compiling the same query document twice must produce the same statement string and the same argument order, whatever order the JSON keys arrived in and whatever Go's map iteration does that run.

## The decision

Every place the compiler walks a map, the keys are sorted first. That is two
places: the filter object's keys (`compiler.node`) and an operator object's keys
(`compiler.operators`). The rest of the pipeline is already order-preserving —
`Options.Filter` is a slice, `Options.Sort` is a slice, the schema's field list
is a slice in declaration order.

The generator does the same for its own output (`generator.load` sorts model
names, `internal/codegen/render.go` sorts imports).

## Why

Go randomises map iteration on purpose. Without the sort, the same JSON document
produces a different `WHERE` clause on every run — same meaning, different text,
different placeholder numbering, different argument order.

That costs four things, in increasing order of how much they hurt:

- **Test assertions.** A test that asserts on the rendered SQL is the clearest
  way to pin a rendering decision, and it is impossible to write against a
  randomised clause. Untestable SQL is not worth having; the whole `crud/query/` test
  suite depends on this.
- **Prepared-statement and plan caches.** PostgreSQL's plan cache and every
  connection pooler key on the statement text. A randomised clause is a cache
  miss every time, and it fills the cache with variants.
- **Diffing.** "This deploy changed the query" is answerable only if the query
  is stable.
- **Debugging.** A statement copied from a log reproduces the report.

The cost of sorting is a `sort.Strings` over the keys of one JSON object,
per object. That is not a number worth optimising against any of the above.

Note what is *not* claimed: the order is sorted, not document order. A client
that writes `{"views": …, "title": …}` gets `title` first in the SQL. The
document is order-insensitive; the statement is not, and the tie is broken the
same way every time.

## What it forbids

- Do not iterate a map into SQL, an argument list, or generated source without
  sorting. This is the whole decision.
- Do not switch the compiler to "document order" by decoding into an ordered
  structure. Sorted is already deterministic and is one line; document order
  would be deterministic too but would make two equivalent documents produce
  different statements, which loses the cache benefit.
- Do not make relation aliases (`rx1`, `rx2`, …) depend on anything but the
  order the writer walks the tree.
- Do not add a map to `Options`. `RelationScopes` holds maps, but they are
  consulted by key lookup (`RelationScopes.At`), never iterated into output.

## Where it lives

- `crud/query/filter.go:compiler.node` — sorts the filter object's keys, with the
  reason in the comment above it.
- `crud/query/filter.go:compiler.operators` — sorts an operator object's keys.
- `crud/options.go:Options` — `Filter`, `Sort`, `Preloads`, `Fields` are all
  slices.
- `crud/meta.go:buildSchema` — the primary key sorts first, the rest keep
  declaration order; `Field.Ordinal` records it.
- `crud/predicate.go:writer.nextAlias` — aliases are numbered by walk order.
- `internal/codegen/codegen.go:generator.load` — `sort.Strings(g.order)`.
- `internal/codegen/render.go:generator.render` — sorts the import block.

## Proven by

- `TestTheSameDocumentAlwaysCompilesToTheSameStatement` in `crud/query/edge_test.go`
  — a document with eleven top-level keys, three nested operator objects, two
  relation hops and a many-to-many, asserted against the exact expected string
  *and* the exact argument slice. Many keys is the case that catches a forgotten
  sort, because two keys pass by luck half the time.
- `TestDeterministicOutput` in `crud/query/query_test.go`.
- `TestKeyOrderDoesNotFollowTheDocument` in `crud/query/compile_test.go` — states the
  half that surprises people: sorted order, not document order.
- `TestOutputIsByteIdenticalAcrossRuns` in `internal/codegen/codegen_test.go` — the
  generator half.
- `TestGeneratedFileIsUpToDate` in `_examples/example/blog/blog_test.go` and
  `TestTheGeneratedStoresAreUpToDate` in `test/codegen/codegen_test.go` —
  these only work because the generator is deterministic; a nondeterministic one
  would make them flap.

## See also

[[D-013]] [[D-018]] [[D-020]]
