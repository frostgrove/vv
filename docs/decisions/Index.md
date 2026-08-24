# Decisions & invariants

This directory pins the things that could reasonably have gone the other way: a
design choice with a real alternative, an invariant the code depends on but
cannot express in a type, and the owner's own corrections. Every file names the
failure mode that made the answer what it is, and says what a future change must
not do.

What does not belong here: how to use a feature (that is
`docs/usage-guides/`), what a function does (that is the doc comment), or a
change with no alternative worth naming.

## How to use this directory

**Read a decision before changing code when** you are about to touch anything
listed in its *Where it lives* section, or when a piece of code looks wrong in a
way that is easy to "fix". The security, relation-narrowing and DISTINCT rules in
particular all look like bugs from a distance and are not. If a change would
violate an *Invariant* line, the change is wrong or the decision needs
superseding — not both quietly at once.

**Add a decision when** you make a choice whose opposite is defensible, or when
you fix a bug whose fix could plausibly be reverted by someone who does not know
what it prevented. Give it the next free number, never reuse one, and mark a
replaced decision `superseded by D-0NN` rather than deleting it.

**Mark it `open`** when the tension is real and unresolved. An open decision
still says what not to do while it is open — see D-024 through D-027.

## Index

| ID | Invariant (one line) | Status | Area |
|----|----------------------|--------|------|
| [D-001](D-001-two-parameter-seam-three-parameter-facade.md) | A middleware is declarable with `[M, ID]` only, so call sites carry no explicit generics | accepted | core seam |
| [D-002](D-002-opt-models-three-states.md) | Absent, explicit null and value are three distinct outcomes for a nullable column | accepted | core seam |
| [D-003](D-003-closed-predicate-ast.md) | The predicate AST is closed; only `crud.Raw` emits caller text, and its markers must match its arguments | accepted | querying |
| [D-004](D-004-where-ands-never-replaces.md) | `crud.Where` appends; no option may remove or weaken a predicate another option added | accepted | querying |
| [D-005](D-005-relation-filter-is-a-correlated-exists.md) | A relation filter must not change the outer statement's cardinality | accepted | querying |
| [D-006](D-006-preload-is-a-batched-second-query.md) | One statement per relation per level; a preload refuses pagination | accepted | querying |
| [D-007](D-007-narrowing-crosses-a-relation-only-when-declared.md) | A scope covers its own `FROM`; the far side of a relation is narrowed only where declared | accepted | security |
| [D-008](D-008-out-of-scope-is-404-not-403.md) | A row hidden by a scope is `ErrNotFound`, never a denial | accepted | security |
| [D-009](D-009-context-executor-capture-is-unconditional.md) | `WithExecutor` reaches every repository; only `WithExecutorFor` restricts it | accepted | transactions & datasources |
| [D-010](D-010-update-is-load-diff-write.md) | `Update` writes only what changed, and locks only inside a transaction | accepted | writes |
| [D-011](D-011-save-is-jpa-shaped.md) | No key inserts, a key upserts, an unset `noauto` key is `ErrMissingID` | accepted | writes |
| [D-012](D-012-put-replaces-never-creates.md) | `PUT /:id` on a database-generated key 404s for an id that names no row | accepted | HTTP |
| [D-013](D-013-unknown-field-is-a-rejection.md) | Every field path resolves or the whole request fails before any SQL | accepted | querying |
| [D-014](D-014-sql-is-deterministic.md) | The same request compiles to byte-identical SQL and the same argument order | accepted | querying |
| [D-015](D-015-errors-are-sentinels.md) | Every branchable failure is reachable with `errors.Is` against a `crud` sentinel | accepted | errors |
| [D-016](D-016-one-module-crud-stays-stdlib-only.md) | One `go get`, no `replace`; `crud/` imports only the standard library | accepted | process & tooling |
| [D-017](D-017-orm-go-side-behaviour-does-not-run.md) | An rx-crud write is exactly the statement rx-crud built; no ORM builder, hook or privacy rule runs | accepted | interop |
| [D-018](D-018-dtos-and-metamodels-are-generated.md) | `rxcrud_gen.go` is generated output, reproducible from the model source | accepted | process & tooling |
| [D-019](D-019-dialect-differences-are-not-observable.md) | The same call answers the same on every engine, except for four named differences | accepted | dialects |
| [D-020](D-020-tests-are-the-specification.md) | A test that could pass vacuously carries a control case that fails without the behaviour | accepted | process & tooling |
| [D-021](D-021-magic-is-preferred-to-go-orthodoxy.md) | Boilerplate is removed from the consumer; the magic must fail at build or start-up, never at request time | accepted | philosophy |
| [D-022](D-022-the-handler-takes-an-interface.md) | The handler holds `Repository[M, ID, U]` and never reaches past it | accepted | HTTP |
| [D-023](D-023-usage-guides-lead-with-what-you-get.md) | Both guides open with the resulting API; setup is Part II | accepted | docs |
| [D-024](D-024-distinct-and-the-forced-primary-key.md) | A `DISTINCT` query produces a valid statement or a readable refusal — never a 500, never a silent no-op | **open** | querying |
| [D-025](D-025-mapkey-collapses-non-comparable-keys.md) | Both ends of a relation agree on a key, and two different keys never collide | **open** | relations |
| [D-026](D-026-deleteall-fetches-victims-with-caller-options.md) | Every row a gated filtered write touches was shown to `Inspect` first | **open** | security |
| [D-027](D-027-intx-cross-database-capture-is-documented-not-enforced.md) | A repository never runs on a database it was not bound to, unless the application said so | **open** | transactions & datasources |
| [D-028](D-028-a-cursor-is-the-sort-tuple.md) | A cursor may only be used with the sort it was made for, and only when that sort ends in the primary key | accepted | querying |
| [D-029](D-029-aggregates-live-on-the-seam.md) | A summary read carries every narrowing a row read carries, and no client can compose one | accepted | querying, security |
| [D-030](D-030-a-new-verb-on-the-seam-is-a-decorator-obligation.md) | Every method added to `crud.Core` is overridden by the gate or has a written reason not to be | accepted | core seam, security |
| [D-031](D-031-soft-delete-is-a-statement-not-a-decorator.md) | Declaring a soft delete declares both the stamp and the read filter | accepted | querying |
| [D-032](D-032-a-replica-never-decides-a-write.md) | A read inside a transaction, or one that decides a write, always goes to the primary | accepted | transactions & datasources |

## By area

**Core seam** — D-001 (two-parameter `Core`, three-parameter `Repo`),
D-002 (`Opt[T]`), D-021 (why any of it is reflective), D-022 (the handler's
interface).

**Querying** — D-028 (cursor pagination), D-003 (closed AST, `Raw`), D-004 (`Where` ANDs),
D-005 (`EXISTS`, not a join), D-006 (batched preload), D-013 (unknown field is a
rejection), D-014 (deterministic SQL), D-024 (**open** — `DISTINCT`).

**Security** — D-007 (narrowing across relations), D-008 (404, not 403),
D-004 (why a scope cannot be peeled off), D-003 (why a caller cannot compose out
of one), D-026 (**open** — `Inspect` and caller paging).

**Writes** — D-010 (load-diff-write, locking, `version`), D-011 (`Save` is
JPA-shaped), D-012 (PUT does not create), D-002 (three-state DTO fields).

**Transactions & datasources** — D-009 (unconditional capture, opt-in scoping),
D-019 (dialect differences), D-027 (**open** — cross-database capture).

**HTTP** — D-012 (PUT), D-022 (interface, not struct), D-015 (error → status),
D-013 (400 for an unknown field).

**Interop with an ORM** — D-017 (Go-side behaviour does not run), D-009 (how the
transaction is shared), D-018 (`-types`, `-into`, `-import`).

**Errors** — D-015 (the sentinel list and the HTTP mapping).

**Dialects** — D-019 (what is hidden and what is observable), D-011 (the upsert
forms), D-010 (why MySQL re-reads).

**Relations** — D-005 (filters), D-006 (preloads), D-007 (narrowings),
D-025 (**open** — key normalisation).

**Process & tooling** — D-016 (one module, stdlib-only core),
D-018 (codegen), D-020 (tests as the specification), D-014 (deterministic
output).

**Philosophy & docs** — D-021 (magic over orthodoxy), D-023 (guides lead with the
result), D-020 (what a test is for).

## Open tensions

- **[D-024](D-024-distinct-and-the-forced-primary-key.md) — `DISTINCT`.**
  A bare `?distinct=1` with no `select` projects every column, primary key
  included, so it deduplicates nothing and says so to nobody. Separately, a paged
  `DISTINCT` cannot have a stable tiebreaker, so page 2 of the same query can
  legitimately differ between calls. Three ways out, all with a cost.
- **[D-025](D-025-mapkey-collapses-non-comparable-keys.md) — `mapKey`.**
  A non-comparable relation key falls through to `reflect.Value.String()`, which
  returns a per-type constant, so every such key collides and every parent gets
  every child. Unreachable from any model in the tree today. Fixing it means
  choosing a serialisation inside a package that may not take a dependency
  (D-016), or refusing the declaration.
- **[D-026](D-026-deleteall-fetches-victims-with-caller-options.md) — gated
  filtered writes.** `gate.DeleteAll` and `gate.UpdateAll` fetch the rows they
  will show `Inspect` using the caller's own options, including `Limit`; the
  `DELETE` and `UPDATE` are unlimited. A caller-supplied `Limit(10)` means
  `Inspect` sees ten rows and the statement touches all of them. No test covers
  it.
- **[D-027](D-027-intx-cross-database-capture-is-documented-not-enforced.md) —
  cross-database capture.** An unscoped `crud.WithExecutor` is adopted by every
  repository, including ones bound to another database. `WithExecutorFor` is the
  documented answer and is not enforced. Enforcing it needs a `Source` identity
  that would refuse the foreign executors the whole interop seam depends on.
