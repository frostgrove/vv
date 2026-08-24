# Working on rx-crud

A generic CRUD repository for Go: JPA-shaped semantics, a Specifications /
Criteria API, a security gate, a wire DSL and a Fiber handler — over any driver,
without owning the caller's connection or transaction.

Module: `github.com/shardit-io/go-rx-crud`. One module; `test/` is a second one
so drivers and ORMs never become dependencies of the library.

---

## Read the docs before the code

`docs/` is not a description of this repository written after the fact. It is
where the reasoning lives, and much of it is not recoverable from the source.
**Look there first — every time, not only when stuck.**

| Question | Where |
|---|---|
| Why is it like this? May I change it? | `docs/decisions/` |
| What is a consumer trying to do? What must hold? | `docs/usecases/` |
| Where does this happen? Which files? | `docs/flows/` |
| How does a consumer set this up? | `docs/usage-guides/` |

Each directory has an `Index.md` built for exactly this lookup. Start there,
not with `grep`.

**The lookup order for any task:**

1. `docs/flows/Index.md` — the reverse index maps every source file to the flows
   that touch it. If you are about to edit a file, read its flows first.
2. `docs/decisions/Index.md` — check whether the thing you are about to change
   was decided deliberately. Several were, and the reasoning is not in the code.
3. `docs/usecases/Index.md` — check which guarantees your change touches.

### The one rule that matters most

**A decision doc is binding.** If `docs/decisions/` says something must not
happen, it must not happen — even when the code looks like it would be cleaner
the other way. Most of those decisions exist because the obvious version was
tried and produced a silent bug: a scope that stopped at a preload, a filter that
matched every row, a write that landed in the wrong database.

If you believe a decision is wrong, do not quietly implement around it. Say so,
name the decision, and let the owner decide. Then update the decision doc with
what was chosen — including a rejection.

---

## The three sections and how they link

```
decisions/  D-NNN   why it is this way, what must not change   ← binding
usecases/   UC-NNN  what a consumer needs, what must hold      ← the contract
flows/      FL-NNN  where it happens, in which files           ← the map
```

Linking rules, and they are deliberate:

- **A use case links only to flows.** No file paths, no function names, no
  package names in a use-case body. A flow doc goes stale when a file moves; a
  use case only goes stale when the product changes. Keep that property.
- **A flow is the only place file paths and symbols appear.** If you need to
  say where something lives, you are writing or updating a flow.
- **A decision may be referenced from anywhere** and links to the flows and use
  cases it constrains.

Cross-references use `[[D-007]]`, `[[UC-003]]`, `[[FL-002]]`.

---

## Keeping the docs consistent — do this without being asked

Documentation drift is a defect here, not untidiness: these files are what the
next agent trusts instead of re-deriving. Treat a stale doc exactly like a
failing test.

**Update in the same change as the code. Never "later".**

| What you changed | What you must update |
|---|---|
| A call path, or moved/renamed a file or symbol | The flow(s) that name it, including the file table and the reverse index in `docs/flows/Index.md` |
| Observable behaviour a caller can see | The use case whose **What must hold** covers it — and if no use case does, that is itself worth reporting |
| Anything a decision doc forbids, permits or explains | That decision. If the answer changed, mark the old one `superseded by D-NNN` and write the new one; do not edit history into agreement |
| Added a public API | The flow that exercises it, the use case it serves, and a decision if the shape was contentious |
| Resolved something marked `Status: open` | Flip the status, say what settled it, and remove it from the Open tensions list in the index |
| Added or changed a test that pins an invariant | The **Proven by** section of the decision, and **Tests that walk this flow** |
| Setup steps a consumer follows | `docs/usage-guides/` — both guides, they are parallel by design |

**Also:** when you add a doc, add its row to the directory's `Index.md` in the
same change. An index that does not list a file is worse than a missing file —
an agent trusts the index and stops looking.

**When you finish a task, do a consistency pass**: re-read the docs you touched
and confirm every file path, symbol and test name in them still exists. A doc
that names a symbol which no longer exists has failed at the one job it has.

---

## Verifying work

The databases run in Docker and are expected to be up (`make up`).

```bash
make unit          # go test ./...            — 12 packages, no database
make integration   # go test -tags=integration ./test/...  — PostgreSQL + MySQL
make test          # both
make vet           # both modules
make fmt           # gofmt
make generate      # regenerate DTOs and metamodels
```

Before reporting a task done: unit green, integration green **twice in a row**
(a test that passes once and fails on rerun is a real defect), `gofmt -l` silent,
`go vet` clean.

If the integration suite fails to connect, the container died — `make up` and
retry. The suite bootstraps its own schema from empty.

---

## Writing tests here

Tests are the specification; see `[[D-020]]`.

- **A test that would still pass if the feature were deleted is a liability.**
  Sanity-check a new test by breaking the library, watching it fail, and
  restoring. Say in your report that you did.
- **Put a control case next to any test that could pass vacuously.** The pattern
  is in `test/integration/gate_relscope_test.go`: the "not declared" subtest
  asserts the leak *is* there without the declaration, so if something else ever
  closes it the control fails and tells you the positive test now proves nothing.
- **Never `t.Parallel()` in `test/integration`** — every test shares the same
  physical tables.
- Compare errors with `errors.Is` against the exported sentinels, never by
  string.
- The failure message states what broke in plain words, not `got != want`.

---

## House style

The code has a voice. Match it rather than averaging toward generic Go.

- **Comments say why, never what.** A comment that restates the code is worse
  than no comment. The good ones here name the failure mode that made the code
  take its shape.
- Plain and direct. No "simply", "just", "easily". Short sentences.
- Prefer the boring construct. The magic in this library is deliberate and
  concentrated (reflection over models, codegen, type inference at the seam) —
  see `[[D-021]]`. Everywhere else, be ordinary.
- `crud/` imports the standard library only. Keep it that way.

---

## Layout

```
crud/                       core: contracts, metadata, relations, predicates, Opt, pagination
repo/basic/                 the plain repository: the layer that speaks SQL
repo/decorators/specs/      JPA Specifications + Criteria API + metamodel
repo/decorators/security/   row-level scope, authorization, per-entity checks
query/                      the wire DSL: one JSON document -> crud.Options
http/crudfiber/             a full CRUD API on Fiber v3
cmd/rxcrud/                 generates the update DTO and the metamodel from your model
adapter/crudsql/            database/sql — and therefore ent, gorm, sqlx, sqlc, bun
adapter/crudpgx/            pgx v5
crud/crudtest/              an in-memory source for unit-testing repositories
test/                       a separate module: integration suite, ent/gorm fixtures
docs/                       decisions, use cases, flows, usage guides
```
