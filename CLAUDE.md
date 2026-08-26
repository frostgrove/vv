# Working on vv

A generic CRUD repository for Go: JPA-shaped semantics, a Specifications /
Criteria API, a security gate, a wire DSL and a Fiber handler — over any driver,
without owning the caller's connection or transaction.

Module: `github.com/shardit-io/vv`, and it has no external dependencies.
Anything that would add one is a module of its own under the same repository —
`crud/http/crudfiber`, `crud/http/crudgin`, `crud/adapter/crudpgx`, `auth/authjwt` —
so a consumer downloads only the binding and driver it imports. `test/` and `_examples/` are two more,
unpublished, so drivers, ORMs and example stacks never become dependencies of
the library. See `[[D-033]]`.

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
| What can this package do, and how is it wired? | `docs/modules/` |
| How does a consumer set this up? | `docs/usage-guides/` |
| What is still open? | `docs/roadmaps/Roadmap.md` |

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
| Added a public API | The flow that exercises it, the use case it serves, `docs/modules/<package>.md`, and a decision if the shape was contentious |
| Changed what a package can do, or an option's name or default | `docs/modules/<package>.md` — it is a consumer's reference and a wrong option name there is a compile error they hit and you did not |
| Closed something the roadmap listed as open | `docs/roadmaps/Roadmap.md` — remove the item rather than annotating it done |
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
make unit          # go test -race ./... in every module, no database
make integration   # go test -race -tags=integration ./test/... — PostgreSQL + MySQL
make test          # both
make vet           # every module
make fmt           # gofmt
make generate      # regenerate DTOs and metamodels
make tidy          # go mod tidy in every module
make examples      # build, vet and test the runnable examples
make vuln          # govulncheck over every module — reaches the network, so not in `check`
make api           # regenerate docs/api/surface.md, the exported-surface baseline
```

`make check` runs the structural checks `go test` cannot: `check-deps`,
`check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-tidy`. Run
it before reporting a task done — several of them fail on things a test suite is
structurally unable to see.

`make vuln` is deliberately outside `check`, because `check` must run offline.
`make release` runs it. Scan in **workspace mode**: with `GOWORK=off` a satellite
cannot resolve the library — there is no tag — and govulncheck reports a loading
error, which reads exactly like a clean scan. Ten of the eleven published modules
were "scanned" that way the first time and none of them actually ran.

`make api` regenerates `docs/api/surface.md`. Nothing checks it and nothing
should: a diff there is a question for a person. After the first tag, a line that
disappears is a breaking change.

`make unit`, `make vet` and `make tidy` loop over `Makefile:MODULES`, which is
**discovered** — `find . -name go.mod` — not a hand-written list. A new module
needs no Makefile edit; it needs `make work` (or `go work use ./<dir>`), a
`replace` line in `test/go.mod` and `_examples/go.mod`, and its docs. A
hand-written list is how a module escapes unit, vet, tidy and release at once,
and this repository has already been bitten by exactly that.

Before reporting a task done: unit green, integration green **twice in a row**
(a test that passes once and fails on rerun is a real defect), `gofmt -l` silent,
`go vet` clean.

**Both suites run under `-race`, and the integration one is why.** It is the only
thing here that touches live drivers, real pools and the concurrency they bring,
and the library holds process-global state every repository over a model shares —
the schema cache, the per-handle catalog. A race in those is not a race between
two repositories but between any two concurrent requests that happen to be first
somewhere, which is the shape that never reproduces in a unit test and did not:
`Relation.resolveDefaults` wrote to a shared `*Relation` outside its `Once` and
was found by reading, not by running.

If the integration suite fails to connect, the container died — `make up` and
retry. The suite bootstraps its own schema from empty.

---

## Writing tests here

Tests are the specification; see `[[D-020]]`.

- **A test that would still pass if the feature were deleted is a liability.**
  Sanity-check a new test by breaking the library, watching it fail, and
  restoring. Say in your report that you did.
- **The wire DSL has fuzz targets** in `crud/query/fuzz_test.go`, and their seed
corpus runs as an ordinary test on every `make unit`. They pin the three
properties everything else rests on: compiling never panics, a refusal produces
no options at all, and nothing the caller wrote reaches the statement as text.
Run a real campaign with `-fuzz FuzzCompileJSON -fuzztime 2m` after touching
anything in `crud/query`.

**Put a control case next to any test that could pass vacuously.** The pattern
  is in `test/integration/gate_relscope_test.go`: the "not declared" subtest
  asserts the leak *is* there without the declaration, so if something else ever
  closes it the control fails and tells you the positive test now proves nothing.
- **Never `t.Parallel()` in `test/integration`** — every test shares the same
  physical tables.
- **A change to one HTTP binding is a change to all three.**
  `crud/http/crudfiber`, `crud/http/crudgin` and `crud/http/crudnet` carry the
  same test names, file for file. If
  a new test only makes sense for one of them, it belongs in that binding's
  `routing_test.go`, and the difference it pins belongs in `[[FL-013]]`.
  **`make check-triplets` is what holds this** — it compares the test names and
  exempts `routing_test.go` and `binding_test.go`, so a difference has to be
  parked in the file that says it is one. Before that check existed the rule
  held by everybody remembering it, and it had already stopped holding.
  **`crud/rpc/crudgrpc` is a fourth transport and is not in that triplet**: it
  carries the subset of those names that is about `port` rather than about HTTP, and
  spells the rest in its own vocabulary because there is no 404 and no `PUT`
  here to name. A test that only makes sense for gRPC belongs in
  `crud/rpc/crudgrpc/`, and the difference it pins belongs in `[[FL-013]]` too.
- **The auth middleware is a second triplet with the same rule.**
  `auth/http/authnet`, `auth/http/authgin` and `auth/http/authfiber` carry the
  same test names file for file, and `auth/rpc/authgrpc` carries the subset that
  is about `port`
  rather than about HTTP. What differs between them goes in that binding's
  `binding_test.go` — the auth triplet's name for `routing_test.go`, because
  what differs there is what the framework does with an error nobody asked it
  about — and is written down in `[[FL-019]]`.
- **An optional interface is never found with a bare type assertion.** A value
  reached through a decorator or a wrapper has lost every method its own
  interface does not name — silently. Use `crud.SourceOf`, `crud.BeginnerOf`,
  `crud.ReadSourceOf` or `crud.KeyOf`, and give any decorator you add a
  `Next()`. `[[D-061]]` has the failure this comes from.
- **Nothing writes to a process-wide logger.** `port.Logger(ctx)` is the seam;
  `log.Printf` in library code is a defect. `[[D-062]]`.
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
- **Package** `crud` imports the standard library only — the package, not the
  subtree: `crud/sqlrepo`, `crud/query` and the rest below it are ordinary
  packages with ordinary dependencies ([[D-016]], and `Makefile:TIER0_STDLIB` is
  what holds it). The whole root *module* still takes no third-party
  dependency at all: a package that needs one becomes a module. `[[D-033]]`.

---

## Layout

The top-level axis is the subsystem; the transport is the second level inside it
([[D-058]]). A package name still carries its prefix even where the path repeats
it — `crud/http/crudfiber` holds `package crudfiber` — because a consumer who
mounts CRUD routes and auth middleware on Gin imports `crudgin` and `authgin` in
the same file ([[D-035]]).

```
crud/                       core: contracts, metadata, relations, predicates, Opt, pagination
├── crudtest/               an in-memory source for unit-testing repositories
├── query/                  the wire DSL: one JSON document -> crud.Options
├── sqlrepo/                the repository that speaks SQL: Define, Bind, the statements
├── decorators/specs/       JPA Specifications + Criteria API + metamodel
├── decorators/security/    row-level scope, authorization, per-entity checks
├── decorators/faults/      the column -> model-field hop, and where the probe is wired in
├── adapter/crudsql/        database/sql — and therefore ent, gorm, sqlx, sqlc, bun
├── adapter/crudpgx/        MODULE — pgx v5
├── catalog/                per-handle schema introspection, four dialects
├── probe/                  one extra statement finds every other violation the payload caused
├── sqlfault/               the tree walk, the integrity gate, fault assembly — what WithFaults takes
├── http/crudhttp/          what is HTTP *and* CRUD: the request shapes, the model hop
├── http/crudnet/           a full CRUD API on net/http — stdlib, so not a module
├── http/crudfiber/         MODULE — a full CRUD API on Fiber v3
├── http/crudgin/           MODULE — the same API on Gin
└── rpc/crudgrpc/           MODULE — the same API on gRPC

auth/                       who the caller is: Principal, Role, Permission, Guard, the 401
├── apikey/                 an Authenticator over a shared secret — stdlib, so not a module
├── authjwt/                MODULE — JWT verification, generic over your claims
├── http/authhttp/          the HTTP half of the auth middleware: the renderer, the refusal
├── http/authnet/           the net/http auth middleware — stdlib, so not a module
├── http/authgin/           MODULE — the Gin auth middleware
├── http/authfiber/         MODULE — the Fiber auth middleware
└── rpc/authgrpc/           MODULE — the gRPC auth interceptors

port/                       the transport-neutral half: commands, Service, Mapper, the path chain
└── porthttp/               the HTTP projection of the error contract: the status table, the
                            envelope, the Renderer seam — every subsystem's, not CRUD's

remote/                     the consuming half: another service's resource, held as a port.Repository
└── remotehttp/             the HTTP client transport: a remote.Call becomes a request

errs/                       the error contract: Code, Kind, Path, Violation, Fault, the SPI
└── sqlerr/                 a driver error becomes a code, one table per dialect

utils/                      for the consumer's application, never for the library
├── vvflag/                 one typed flag, without owning the command line
├── vvcfg/                  MODULE — a config struct, loaded and validated at start-up
└── vvdb/                   one config -> a DSN or a *sql.DB, four engines; who opens the connection
    └── dbpgx/              MODULE — the same config, a pgx pool

cmd/vv/                     generates the update DTO and the metamodel from your model
internal/codegen/           what cmd/vv is a front end for
docs/                       modules, decisions, use cases, flows, usage guides, the roadmap

test/                       MODULE (unpublished) — integration suite, ent/gorm fixtures
_examples/                  MODULE (unpublished) — runnable examples, one per stack
```

`utils/` is the one name in the tree chosen for what it is not: a subsystem. Its
boundary is a single line — **nothing under `utils/` imports `crud/`, `auth/`,
`port/` or `remote/`.** A package that needs to is not a utility, and it moves to
the subsystem it belongs to. Without that line `utils/` collects half the
repository inside a year. `make check-utils` is what holds it.

`vvdb` is there despite carrying a satellite module beneath it, and that is the line working
rather than bending: [[D-057]] already forbids it `crud`, `errs`, and any call
from inside the repository path. What the boundary measures is the import graph,
not the package count.

`_examples/` starts with an underscore, so the Go toolchain ignores it at the
root: `make unit` does not build it and `make examples` does.
