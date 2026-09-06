# TENANCY — D0 baseline (verified by the lead, not by a subagent)

Every line below was produced by a command run in
`/home/user/ws/gd/lease/frostgrove/vv/framework` on this tree.

## Suites

| Command | Result |
|---|---|
| `go test ./tenancy/... ./scripts/...` | **green** — 6 tenancy packages + `scripts` |
| `go test -race -tags=integration -run 'Tenant\|Tenancy\|LifecycleThatDoesNotAdmit' ./integration/...` (from `test/`, `VV_PG_DSN` → 127.0.0.1:55433) | **green**, and it genuinely reached PostgreSQL |
| `make check` | **FAILS**, on `check-tidy` only |
| `make check` — `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces` | pass |

`make check`'s only failure is `check-tidy` in `app/http/appfiber`,
`auth/access/accessjwt`, `.../revokeredis`, `.../revokeredisfx` — the four modules
`TENANCY_PLAN.md` already records as a pre-existing `[low][deferred]` failure.
It is not tenancy's.

## The live suite cannot reach its own database by default — INFRA, [high]

`docker-compose.yml:22` publishes the suite's PostgreSQL on **55432**, and
`test/corpus/corpus.go:19` defaults `VV_PG_DSN` to
`postgres://vv:vv@127.0.0.1:55432/vv`. On this machine 55432 is held by
`analyzer4-postgres-1` — a different project, `POSTGRES_USER=analyzer`,
`POSTGRES_DB=analyzer`. The `vv` compose project's own postgres service is not
running; a hand-made `vv-tenancy-pg` (vv/vv/vv, PG 17) sits on **55433** instead.

So `make integration` as written today does not test this repository. Credentials
differ, so it fails to connect rather than writing into the analyzer database —
but per `CLAUDE.md` that failure reads as "the container died, `make up` and
retry", which is exactly the misreading that has already cost this project a
review cycle (`TENANCY_PLAN.md`, "What green means here, exactly").

Live evidence in this audit was therefore obtained with an explicit
`VV_PG_DSN=postgres://vv:vv@127.0.0.1:55433/vv?sslmode=disable`. Any tenancy
result claimed from `make integration` without that override should be
disbelieved until the port is confirmed.

## Live coverage of the priority topology is two functions

`test/integration/tenancy_test.go` is the only live-database proof the shared-row
topology has, and it is **PostgreSQL only** — it builds its own
`Target{Name: "postgres", ...}` (`tenancy_test.go:50`) rather than walking the
dialect matrix the rest of the suite uses. MySQL, MariaDB and SQLite never
execute a tenant-narrowed statement.

`TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable` covers, in five
subtests: `GetByID` of a foreign row, `Count`/`Exists` of a foreign row,
`Update`/`Delete` by guessed id, a create into another tenant, and the tenant's
own rows.

Not covered by any live test, on any dialect: `UpdateAll`, `DeleteAll`,
`Restore`, `SaveAll`, `InsertBatch`, `Aggregate`, `First`, `ExistsUnscoped`,
assigned-key `Save`/upsert, cursor pagination, `Through` ownership, and relation
preloads at depth ≥ 1. `TestATokensTenantClaimNarrowsTheStatement`
(`test/integration/auth_jwt_test.go:99`) does run the full dialect matrix
(postgres/mysql/mariadb, 7 subtests each) — but it imports no tenancy package at
all. It exercises the base `security` gate driven by a JWT claim, so it proves
nothing about `tenancyrow`.

## Tenancy has no transport-layer integration — [high], DX

```
grep -rn "tenanc" --include="*.go" auth/ port/ app/ crud/http/ runtime/   → no matches
```

Nothing in any HTTP or RPC binding knows how to turn a request into a bound
tenant. The consumer-facing answer to "where does the tenant come from" is
`authority.Bind(ctx, class)` and nothing more: `docs/modules/en/tenancy.md:64`
shows the call with no surrounding middleware, and
`_examples/tenancy-sharedrow/main.go` fabricates the request end with a
hand-rolled `type requestedTenant struct{}` context key it defines itself
(`main.go:69`) — a shape no consumer can copy into a Fiber/Gin/net-http stack
without inventing the missing half.

This is the single most common question a team adopting the extension asks, and
neither the docs, the example, nor the code answers it.

## Working tree

`tenancy/`, `scripts/tenancy_test.go`, `test/integration/tenancy_test.go`,
`_examples/tenancy-sharedrow/`, `docs/modules/{en,ru}/tenancy.md`, `D-116`,
`D-117`, `D-118`, `FL-033`, `FL-035`, `docs/ai/usecases/modules/tenancy/` and
`docs/ai/usecases/modules/jobs/` are all **untracked**. 35 further tracked files
are modified. This is uncommitted in-progress work; git history says nothing
about it.
