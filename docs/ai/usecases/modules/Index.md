# Module use cases and readiness sweeps

`UC-NNN` files are settled consumer contracts and their statuses are binding.
`<Module>.md` files are fallible, consumer-first and source-checked readiness
sweeps. Read the UC for the promise and the sweep for current release confidence.

The order is for newcomers: core, decorators, request/transports, auth, errors,
adapters, tooling, utilities.

| Module | Import paths | Sweep | UC files | Verdict |
|---|---|---|---|---|
| crud | `github.com/frostgrove/vv/crud` | [Crud](crud/Crud.md) | [UC-010](crud/UC-010-adopt-an-existing-orm-model.md) | not ready |
| sqlrepo | `github.com/frostgrove/vv/crud/sqlrepo` | [Sqlrepo](sqlrepo/Sqlrepo.md) | [UC-003](sqlrepo/UC-003-partial-update-absent-vs-null.md) · [UC-005](sqlrepo/UC-005-run-repository-work-in-an-orm-transaction.md) · [UC-006](sqlrepo/UC-006-query-and-sort-across-relations.md) · [UC-008](sqlrepo/UC-008-write-many-rows-in-one-statement.md) · [UC-009](sqlrepo/UC-009-survive-concurrent-writers.md) · [UC-012](sqlrepo/UC-012-talk-to-more-than-one-database.md) · [UC-016](sqlrepo/UC-016-hide-rows-permanently-at-the-repository-level.md) | not ready |
| cache | `github.com/frostgrove/vv/cache` · `github.com/frostgrove/vv/cache/cachememory` | [Cache](cache/Cache.md) | [UC-024](cache/UC-024-cache-recreatable-values-without-unbounded-work.md) | ready for bounded process caching |
| health | `github.com/frostgrove/vv/health` · `github.com/frostgrove/vv/health/healthfx` | — | [UC-025](health/UC-025-say-whether-this-replica-should-take-traffic.md) | not swept |
| runtime | `github.com/frostgrove/vv/runtime` · `github.com/frostgrove/vv/runtime/runtimefx` | — | [UC-026](runtime/UC-026-run-background-work-without-losing-a-worker-silently.md) | not swept |
| app | `github.com/frostgrove/vv/app` · `github.com/frostgrove/vv/app/module` · `github.com/frostgrove/vv/app/appfx` | — | [UC-027](app/UC-027-run-one-codebase-as-an-api-a-worker-and-a-seed-command.md) | not swept |
| query | `github.com/frostgrove/vv/crud/query` | [Query](query/Query.md) | [UC-002](query/UC-002-let-an-untrusted-client-query.md) | not ready |
| specs | `github.com/frostgrove/vv/crud/decorators/specs` | [Specs](specs/Specs.md) | [UC-007](specs/UC-007-write-typed-compile-checked-queries.md) | not ready |
| security | `github.com/frostgrove/vv/crud/decorators/security` | [Security](security/Security.md) | [UC-004](security/UC-004-isolate-tenants.md) · [UC-020](security/UC-020-authorize-without-a-policy-per-endpoint.md) | not ready |
| faults | `github.com/frostgrove/vv/crud/decorators/faults` · `github.com/frostgrove/vv/crud/sqlfault` · `github.com/frostgrove/vv/crud/probe` · `github.com/frostgrove/vv/crud/catalog` | [Faults](faults/Faults.md) | [UC-017](faults/UC-017-get-every-error-for-one-payload-at-once.md) | not ready |
| port | `github.com/frostgrove/vv/port` · `github.com/frostgrove/vv/port/porthttp` | [Port](port/Port.md) | [UC-013](port/UC-013-business-rules-between-handler-and-repository.md) | not ready |
| crudhttp | `github.com/frostgrove/vv/crud/http/{crudhttp,crudnet,crudfiber,crudgin}` | [Crudhttp](crudhttp/Crudhttp.md) | — | not ready |
| crudgrpc | `github.com/frostgrove/vv/crud/rpc/crudgrpc` | [Crudgrpc](crudgrpc/Crudgrpc.md) | — | not ready |
| remote | `github.com/frostgrove/vv/remote` · `github.com/frostgrove/vv/remote/remotehttp` | [Remote](remote/Remote.md) | [UC-018](remote/UC-018-consume-another-services-crud-api.md) | not ready |
| auth | `github.com/frostgrove/vv/auth` · `github.com/frostgrove/vv/auth/authjwt` · `github.com/frostgrove/vv/auth/apikey` | [Auth](auth/Auth.md) | [UC-019](auth/UC-019-authenticate-a-request-and-let-the-repository-see-who-it-is.md) | not ready |
| authhttp | `github.com/frostgrove/vv/auth/http/{authhttp,authnet,authgin,authfiber}` · `github.com/frostgrove/vv/auth/rpc/authgrpc` | [Authhttp](authhttp/Authhttp.md) | — | not ready |
| errs | `github.com/frostgrove/vv/errs` · `github.com/frostgrove/vv/errs/sqlerr` | [Errs](errs/Errs.md) | — | not ready |
| adapters | `github.com/frostgrove/vv/crud/adapter/{crudsql,crudpgx}` | [Adapters](adapters/Adapters.md) | — | ready with gaps |
| crudtest | `github.com/frostgrove/vv/crud/crudtest` | [Crudtest](crudtest/Crudtest.md) | [UC-011](crudtest/UC-011-test-repository-behaviour-without-a-database.md) | not ready |
| codegen | `github.com/frostgrove/vv/cmd/vv` · `github.com/frostgrove/vv/internal/codegen` | [Codegen](codegen/Codegen.md) | [UC-014](codegen/UC-014-keep-generated-artefacts-in-sync.md) | not ready |
| utils | `github.com/frostgrove/vv/utils/{vvflag,vvcfg}` | [Utils](utils/Utils.md) | — | not ready |
| vvdb | `github.com/frostgrove/vv/utils/vvdb` · `github.com/frostgrove/vv/utils/vvdb/dbpgx` | [Vvdb](vvdb/Vvdb.md) | [UC-021](vvdb/UC-021-configure-a-database-once-in-one-file.md) | not ready |
| vvgoose | `github.com/frostgrove/vv/utils/vvgoose` | [Vvgoose](vvgoose/Vvgoose.md) | [UC-022](vvgoose/UC-022-run-and-generate-database-migrations.md) | ready |
