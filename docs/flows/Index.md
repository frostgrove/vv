# Flows

A flow is what actually happens in this codebase, end to end, with the files it
happens in: where a PATCH body turns into an `UPDATE` statement, and what is in
the way. Decisions ([[D-001]] and friends) say *why*. Use cases (`UC-NNN`) say
what a consumer wants. Flows say *where and how*.

**This is the only section that names files and symbols.** If you are writing
down where something lives, you are writing a flow. If you find a file path in a
decision or a use case, it belongs here instead.

Line numbers in these documents were correct when they were written. Treat them
as a starting point for a search, not as an address — the symbol name is the
thing that has to still exist.

## How to use this directory

Before you change something, read the flow that covers it. The **By file** table
below is the fastest way in: find the file you are about to edit and read every
flow it appears in, because a path you are not thinking about probably runs
through it.

| Before you touch… | Read |
|---|---|
| the query DSL, allow-lists, paging, the COUNT | [[FL-001]] |
| the update planner, `Opt`, the version column, the MySQL read-back | [[FL-002]] |
| `Save`, upserts, generated keys, `immutable` columns | [[FL-003]] |
| tags, schema reflection, relations, what fails at start-up | [[FL-004]] |
| the predicate writer, aliases, nested filters and sorts | [[FL-005]] |
| preloads, batching, how children are written into parents | [[FL-006]] |
| the security gate on reads | [[FL-007]] |
| the security gate on writes | [[FL-008]] |
| `context` executors, `InTx`, adapters, savepoints | [[FL-009]] |
| `cmd/vv` and anything generated | [[FL-010]] |
| sentinels, HTTP statuses, what a 500 may say | [[FL-011]] |
| operators, coercion, timestamps, the two front doors | [[FL-012]] |
| the Gin or net/http binding, mounting, or anything the three bindings do differently | [[FL-013]] |

**A code change that alters a path must update its flow document in the same
change.** Not afterwards, not in a follow-up. A flow that describes a path the
code no longer takes is worse than no flow: it is read as current and it is
wrong. If the change makes a step disappear, delete the step; if it adds a trap,
add it to *Where the decisions bite* or *Traps*.

When adding a flow: `FL-NNN-kebab-slug.md`, next number free, and add it to both
tables here — the index and the reverse index.

## Index

| ID | Flow | Entry point | Implements |
|----|------|-------------|-----------|
| [FL-001](FL-001-list-request-to-rows.md) | A list request from wire to rows | `http/crudfiber/handler.go:List` / `:Query` | [[UC-001]] [[UC-002]] |
| [FL-002](FL-002-patch-becomes-an-update.md) | A PATCH becomes an UPDATE | `http/crudfiber/handler.go:Update` | [[UC-003]] [[UC-009]] |
| [FL-003](FL-003-save-insert-versus-upsert.md) | Save: insert versus upsert | `repo/basic/repository.go:Save` | [[UC-001]] [[UC-009]] |
| [FL-004](FL-004-declaration-what-define-validates.md) | Declaration: what `basic.Define` validates and when | `repo/basic/blueprint.go:Define` | [[UC-010]] [[UC-014]] [[UC-016]] |
| [FL-005](FL-005-relation-filter-becomes-a-correlated-exists.md) | A relation filter becomes a correlated EXISTS | `crud/predicate.go:writer.leaf` | [[UC-006]] [[UC-004]] |
| [FL-006](FL-006-preload-becomes-batched-second-queries.md) | A preload becomes batched second queries | `crud/preload.go:RunPreloads` | [[UC-006]] [[UC-004]] |
| [FL-007](FL-007-a-read-through-the-security-gate.md) | A read through the security gate | `repo/decorators/security/security.go:gate.GetByID` | [[UC-004]] [[UC-016]] |
| [FL-008](FL-008-a-write-through-the-security-gate.md) | A write through the security gate | `repo/decorators/security/security.go:gate.Save` | [[UC-004]] [[UC-008]] |
| [FL-009](FL-009-transactions-joining-opening-which-database.md) | Transactions: joining, opening, and which database | `crud/executor.go:InTx` | [[UC-005]] [[UC-012]] |
| [FL-010](FL-010-codegen-model-to-dto-and-metamodel.md) | Codegen: a model becomes a DTO and a metamodel | `internal/codegen.Run` | [[UC-014]] [[UC-010]] [[UC-007]] |
| [FL-011](FL-011-an-error-becomes-an-http-status.md) | An error becomes an HTTP status | `http/crudhttp/errors.go:Status` | [[UC-015]] |
| [FL-012](FL-012-a-wire-value-becomes-a-go-value.md) | A wire value becomes a Go value | `query/coerce.go:decodeValue` / `:coerceString` | [[UC-002]] [[UC-006]] |
| [FL-013](FL-013-a-request-through-another-binding.md) | A request through the Gin and net/http bindings | `http/crudgin/handler.go:List` / `http/crudnet/handler.go:List` | [[UC-001]] [[UC-002]] [[UC-013]] [[UC-015]] |

## By file — which flows touch this file

| File | Flows |
|---|---|
| `adapter/crudpgx/conflict.go` | FL-003, FL-011 |
| `adapter/crudpgx/crudpgx.go` | FL-009, FL-011 |
| `adapter/crudsql/conflict.go` | FL-003, FL-011 |
| `adapter/crudsql/crudsql.go` | FL-009 |
| `cmd/vv/main.go` | FL-010 |
| `internal/codegen/codegen.go` | FL-010 |
| `internal/codegen/render.go` | FL-010 |
| `crud/access.go` | FL-001, FL-003, FL-004, FL-006, FL-008 |
| `crud/dialect.go` | FL-002, FL-003, FL-009 |
| `crud/errors.go` | FL-002, FL-003, FL-009, FL-011 |
| `crud/executor.go` | FL-002, FL-009 |
| `crud/meta.go` | FL-002, FL-003, FL-004, FL-010, FL-012 |
| `crud/options.go` | FL-001, FL-007, FL-008 |
| `crud/opt.go` | FL-002 |
| `crud/page.go` | FL-001 |
| `crud/predicate.go` | FL-005, FL-007, FL-012 |
| `crud/preload.go` | FL-006 |
| `crud/relation.go` | FL-001, FL-004, FL-005, FL-006 |
| `crud/render.go` | FL-001, FL-004, FL-005 |
| `crud/repo.go` | FL-002, FL-007 |
| `crud/scope.go` | FL-004, FL-005, FL-006, FL-007 |
| `crud/update.go` | FL-002, FL-004, FL-008, FL-010 |
| `*/vv_gen.go` — nine checked-in files under `test/` and `_examples/` | FL-010 |
| `http/crudfiber/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012 |
| `http/crudfiber/options.go` | FL-002, FL-011 |
| `http/crudgin/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012, FL-013 |
| `http/crudgin/options.go` | FL-002, FL-011, FL-013 |
| `http/crudnet/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012, FL-013 |
| `http/crudnet/options.go` | FL-002, FL-011, FL-013 |
| `http/crudhttp/errors.go` | FL-011, FL-013 |
| `http/crudhttp/model.go` | FL-003, FL-013 |
| `http/crudhttp/repository.go` | FL-013 |
| `http/crudhttp/request.go` | FL-001, FL-002, FL-012, FL-013 |
| `query/coerce.go` | FL-012 |
| `query/compile.go` | FL-001, FL-006, FL-011 |
| `query/filter.go` | FL-001, FL-005, FL-012 |
| `query/ops.go` | FL-012 |
| `query/querystring.go` | FL-001, FL-012 |
| `query/request.go` | FL-001 |
| `repo/basic/blueprint.go` | FL-003, FL-004, FL-006, FL-007 |
| `repo/basic/repository.go` | FL-001, FL-002, FL-003, FL-004, FL-005, FL-006, FL-007, FL-008, FL-009, FL-011 |
| `repo/decorators/security/policies.go` | FL-004, FL-007, FL-008 |
| `repo/decorators/security/security.go` | FL-002, FL-003, FL-007, FL-008, FL-011 |
| `repo/decorators/specs/errors.go` | FL-011 |
| `repo/decorators/specs/executor.go` | FL-011 |
| `repo/decorators/specs/metamodel.go` | FL-010 |

`repo/basic/repository.go` is in ten of the thirteen. It is the layer everything
else decorates, and almost no change to it is local.

## Not yet written

Two paths exist, are used by a documented use case, and have no flow of their
own. Both are currently covered only in passing by the flows above. Write one
before changing them substantially rather than after.

- **A specification becomes options** — `repo/decorators/specs/`: `Specification`
  composition, `Metamodel` binding at package initialisation, and the
  `FindOne` / `DeleteBy` / `UpdateBy` guards. [[UC-007]] leans on [[FL-010]] and
  [[FL-005]] for it; [[FL-011]] names its sentinels; nothing traces the path.
- **A test double answers a statement** — `crud/crudtest/`: the recorder is how
  almost every unit test in the tree asserts on SQL, so its statement queue,
  scan conversion rules and `Begin` behaviour are worth writing down once.
  [[UC-011]] is about it and points at flows that only use it.
