# VV_MODULES map (repo audit)

Scope: whole repository `/home/user/ws/apps/photon/tmp/frostgrove/framework`.
Date: 2026-09-12. Changes nothing.

This is a **library**, not an application. There is no single `main()` composition root for
a product. Consumers assemble. The library forbids owning a container ([[D-037]]); fx
bindings are satellites ([[D-074]]).

Prior audits in this folder (`TENANCY_*`, `NAIVE_CONTRACTS_AUDIT.md`, `snapshot-0046`) are
**out of scope as evidence**. Re-measure. Tenancy is one subsystem among many.

## What it is

Module path: `github.com/frostgrove/vv`. Go 1.26 workspace (`go.work`). Root `go.mod` has
**no third-party require** ([[D-033]]). Anything that would add one is its own Go module
under the same git repo. `test/` and `_examples/` are unpublished.

~37 satellite `go.mod` files (plus root, test, examples). ~1648 `.go` files. Structural
gates live in `scripts/checks.sh` / `make check`.

## Project rules that override ECONV defaults

Binding docs: `CLAUDE.md`, `docs/ai/decisions/` (D-NNN), `docs/ai/usecases/`,
`docs/ai/flows/`, `docs/modules/en/`. A decision doc outranks a cleaner-looking refactor.

Do **not** report as defects:

- Stdlib-only root + satellite modules for third-party (Fiber, Gin, pgx, fx, Redis, MinIO, OTel).
- No DI container in the root library.
- Package names repeating path prefixes (`crudfiber` in `crud/http/crudfiber`) — [[D-035]].
- `utils/` may not import `crud/`, `auth/`, `port/`, `remote/` — `make check-utils`.
- Optional interfaces found via `crud.SourceOf` / `Next()`, not bare type assertions — [[D-061]].
- Logging only through `port.Logger(ctx)` — [[D-062]].
- HTTP binding triplet (fiber/gin/net) sharing test names — `make check-triplets`.
- No `t.Parallel()` in `test/integration`.
- Comments are exceptional; exported GoDoc is not required.
- In-memory stores (`eventmemory`, `auditmemory`, `cachememory`, `crudtest`) that are real
  implementations, not test fakes of a missing backend.

`scripts/checks.sh` tiers (excerpt):

- `SHARED=(utils)`
- `TIER0=(crud crud/crudtest crud/query errs errs/sqlerr port port/porthttp SHARED)`
- `TIER0_STDLIB=(crud SHARED)` — stdlib + SHARED only for package `crud` (not the subtree)
- `TIER0_SEALED=(errs)`
- `SUBSYSTEMS=(crud auth port remote storage app tenancy event)` — note: `cache`, `jobs`,
  `health`, `runtime`, `audit`, `i18n`, `otel`, `vvdb` exist as top-level packages and may
  or may not be in this array; auditors must check whether `check-tiers` / `check-deps`
  actually cover them.

Contract manifest (closed, [[D-048]]): `crud`, `crud/crudtest`, `crud/query`, `errs`,
`errs/sqlerr`, `port`, `port/porthttp`.

## Bounded contexts / top-level packages

| Path | Role |
|---|---|
| `crud/` | Persistence contract, SQL repo, query DSL, adapters, HTTP/gRPC CRUD, catalog/probe/faults, specs/security |
| `auth/` | Identity: Principal/Guard; JWT; API key; HTTP/gRPC middleware; `access` sessions/RBAC |
| `port/` | Transport-neutral commands, Service, Mapper; `porthttp` status table |
| `remote/` | Consume another service as `port.Repository` |
| `errs/` | Error contract + `sqlerr` dialect tables |
| `app/` | Ordered contribution chain, seed; `app/module` catalog; `appfx`/`appfiber` satellites |
| `tenancy/` | Verified tenant scope + per-seam packages (row, db, jobs, storage, cache) |
| `event/` | Event-sourcing vocabulary; `eventmemory`; `eventpg`; `projection`; `eventtest` |
| `audit/` | Typed audit evidence; memory + pg + crud co-commit |
| `cache/` | Typed cache declarations; `cachememory`; `cachefx` |
| `jobs/` | Durable/background work: memory, pg, redis, fx |
| `storage/` | Object store contract; fs; minio |
| `health/` | Liveness/readiness projections; `healthfx` |
| `runtime/` | Runner/supervisor; `runtimefx`; `runtimecheck` |
| `i18n/` | Separate Go module: MF2 catalogues |
| `otel/` | Separate Go module: OTel adapters |
| `vvdb/` | Open handle from config; `dbpgx`; `lock`/`locksql` |
| `utils/` | SHARED: `Opt[T]`, vvflag; satellites vvcfg, vvgoose |
| `cmd/vv`, `internal/codegen` | Generators |
| `test/` | Integration suite (Postgres + MySQL), unpublished |
| `_examples/` | Runnable stacks, unpublished |

## Composition / entry points

- **Library consumers** wire: adapter (`crudsql`/`crudpgx`) + `sqlrepo.Define` + optional
  decorators + `port` + a transport binding (`crudnet`/`crudfiber`/`crudgin`/`crudgrpc`) +
  optional `auth*` + `app`/`appfx`.
- **CLI:** `cmd/vv` (DTO, metamodel, wire, operations, modules).
- **Examples:** `_examples/` one program per stack.
- **Fx satellites:** `appfx`, `appfiber`, `accessfx`, `cachefx`, `healthfx`, `jobsfx`,
  `jobspgfx`, `runtimefx`, `crudsqlfx`, `storageminiofx`, `revokeredisfx`.
- **Boot gates:** `authhttp.Verify` / router vs access declarations ([[FL-024]], [[D-073]]).

There is no LLM/embedding/model-inference path in this repo.

## State

| Kind | Where |
|---|---|
| Caller DB | Never owned. Adapters take a handle. `vvdb`/`dbpgx` are application plumbing. |
| Schema catalog | Process-global per-handle cache in `crud/catalog` — race-sensitive |
| SQL | `crud/sqlrepo`, `catalog`, `probe`, `sqlfault` |
| Event store | `eventmemory`, `eventpg` (Postgres) |
| Jobs | `jobsmemory`, `jobspg`, `jobsredis` |
| Cache | `cachememory`; declarations in `cache` |
| Object bytes | `storagefs`, `storageminio` |
| Sessions/RBAC | `auth/access` seven tables |
| Audit log | `auditmemory`, `auditpg` |
| Locks | `vvdb/lock`, `locksql` |
| i18n catalogues | `i18n` artifacts |
| Migrations | Goose via `vvgoose`; eventpg schema verification |

Docker: `docker-compose.yml` (Postgres + MySQL). Integration tests bootstrap schema from empty.

## Tests

- `make unit` — `go test -race ./...` in every module, no database.
- `make integration` — `go test -race -tags=integration ./test/...`
- Package tests next to code; fuzz in `crud/query/fuzz_test.go`.
- Binding triplets must share test names (`make check-triplets`).
- Structural: `make check` = deps, tiers, utils, triplets, todo, replaces, tidy.
- `make examples`, `make vet`, `make fmt`.
- `make vuln` is network; outside `check`.

## Documented map of modules (consumer-facing)

`docs/modules/en/Index.md` is the product map. Auditors: if a top-level package is missing
from `SUBSYSTEMS` in `checks.sh` or from that index, that is a finding candidate — verify.

Roadmap still open: `docs/roadmaps/Roadmap.md` (errs as own module, more engines, tenancy
DB-per-tenant, jobs/cache conformance, OTel maximal, factory vocabulary, etc.). Open items
are not automatically defects; unimplemented roadmap is expected.

## How to measure here (Go, not Python)

```bash
# modules
find . -name go.mod -not -path './.git/*'
# size
find <scope> -name '*.go' -not -path '*/testdata/*' | xargs wc -l | sort -rn | head
# imports
go list -f '{{.ImportPath}} {{.Imports}}' <pkgs>
# structural
make check-deps check-tiers check-utils check-triplets
# cycles
go list -f '{{.ImportPath}}' ./...
```

Kernel vs extension pattern in this repo: **stdlib contract package** + **optional satellite
module** (driver/binding/fx). Adding Fiber does not require a kernel diff if the HTTP
projection already exists (`porthttp` / `crudhttp` / `authhttp`). Event kernel is frozen by
`scripts/event_kernel.sha256` (`make check-event-kernel`).

## Out of scope for "fix"

Do not edit code. Write `.agents/artifacts/audit/VV_MODULES_D<n>_AUDIT.md` only
(split letters: D4a, D4b, D5a, D5b as assigned).
