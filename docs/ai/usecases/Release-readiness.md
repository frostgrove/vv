# Release readiness

This is the release-readiness roll-up of the 19 consumer sweeps. Each sweep
invented the consumer journey first and inspected the source and controls second;
the distance is therefore a set of leads to confirm, not findings to act on
blind. The result is not release-ready: several stock or documented paths can
silently widen a read, write the wrong row, authenticate the wrong credential,
or make a configuration mean something different from what its author read.

## Verdict per module

| Module | Verdict | The one thing | Sweep |
|---|---|---|---|
| General | not ready | Stock assembly still leaves policy, query bounds, and error wiring as silent per-resource facts. | [General](general/General.md) |
| Faults | not ready | A probe source can consult a different physical database without an identity check. | [Faults](modules/faults/Faults.md) |
| Security | not ready | A visibly gated repository can be unscoped through a zero policy or nil scope. | [Security](modules/security/Security.md) |
| Sqlrepo | not ready | Permanent scopes replace rather than compose, and `Save` bypasses optimistic locking. | [Sqlrepo](modules/sqlrepo/Sqlrepo.md) |
| Utils | not ready | Configuration input can silently change meaning or be ignored. | [Utils](modules/utils/Utils.md) |
| Remote | not ready | `GetAll` now exhausts bounded pages through cursor edges and refuses inconsistent progress, but `remote.Resource` deliberately cannot be composed with `security.Gate` because it is not a transaction-capable `crud.Core`. | [Remote](modules/remote/Remote.md) |
| Vvdb | not ready | Connection configuration can silently select the wrong endpoint. | [Vvdb](modules/vvdb/Vvdb.md) |
| Auth | not ready | Empty HMAC material is a forgeable authentication configuration. | [Auth](modules/auth/Auth.md) |
| Crud | not ready | A manual ID filter has no consumer-visible bind budget. | [Crud](modules/crud/Crud.md) |
| Crudhttp | not ready | Valid-looking JSON can write a zero model or a different value. | [Crudhttp](modules/crudhttp/Crudhttp.md) |
| Port | not ready | The documented error-install seam does not reach generated routes. | [Port](modules/port/Port.md) |
| Query | not ready | Empty or malformed filters can silently remove or reverse narrowing. | [Query](modules/query/Query.md) |
| Specs | not ready | Natural typed predicates can widen a read or bulk write. | [Specs](modules/specs/Specs.md) |
| Crudgrpc | not ready | `Replace` and the published error/panic/reflection seams still need hardening; unsafe native 64-bit numeric IDs are now refused. | [Crudgrpc](modules/crudgrpc/Crudgrpc.md) |
| Adapters | ready with gaps | Cursor-close errors can still be discarded after an otherwise successful read. | [Adapters](modules/adapters/Adapters.md) |
| Crudtest | not ready | A cancellation test can pass while the fake still records work. | [Crudtest](modules/crudtest/Crudtest.md) |
| Errs | not ready | Invalid public error values can render as apparently valid client responses. | [Errs](modules/errs/Errs.md) |
| Authhttp | not ready | The HTTP mount has no option door for route exemptions or a wholesale renderer. | [Authhttp](modules/authhttp/Authhttp.md) |
| Codegen | not ready | Generation can overwrite authored source outside its intended output. | [Codegen](modules/codegen/Codegen.md) |

## Blockers, worst first

`New?` is **no** only where [Index Gaps](Index.md#Gaps) already records an
equivalent consumer failure. Rows marked “merged” are one repair even though the
consumer reaches it through more than one sweep.

| # | Module | What | Severity | New? | Why it blocks a tag |
|---|---|---|---|---|---|
| 1 | Security | A zero policy or a hand-written scope returning `(nil, nil)` leaves reads unscoped. | blocker | no | A configuration that visibly says `Gate` can return another tenant’s rows with 200. |
| 2 | Sqlrepo | A second table or same-path relation scope replaces the first permanent narrowing. | blocker | yes | Independently safe tenant/visibility declarations can erase one another and leak rows. |
| 3 | Faults | `WithSource` can probe a different physical database than the catalog used to plan the check. | blocker | yes | Another database can decide this endpoint’s availability/existence answer. |
| 4 | Auth | Empty HMAC material starts a parser that accepts publicly forgeable signatures. | blocker | yes | A forgotten secret is an authentication bypass rather than a deploy failure. |
| 5 | Authhttp + General (merged) | Repeated credentials select a first value; General shows that chosen principal becoming generated tenant scope. | serious | yes | Header/metadata ordering can select an otherwise valid caller and tenant view with no ambiguity record. |
| 6 | Crudgrpc | ~~A `Struct` numeric ID at magnitude 2⁵³ or greater is accepted after rounding.~~ | closed | yes | Unsafe numeric spellings are refused; framework gRPC calls carry exact decimal keys. |
| 7 | Sqlrepo | Versioned full-model `Save`/`Replace` does not refuse a stale writer; MySQL may also update a row chosen by another unique key. | blocker | yes | A common replacement route can silently overwrite newer or different-row data. |
| 8 | Specs | Same-path relation conditions can match different children; empty typed `NotIn` can become a whole-table predicate, including bulk writes. | blocker | no | A type-safe-looking query can return too much or rewrite/delete every matching row. |
| 9 | Crudhttp | Absent or top-level-`null` mutation bodies can create or replace a zero model; duplicate JSON keys and null bulk IDs can also change data. | blocker | yes | A proxy or client integration mistake receives success after writing the wrong record. |
| 10 | Remote | Closed for detectable protocol contradictions: `GetAll` reads bounded pages, follows cursor edges, and returns `ErrPartialResult` for malformed progress. | closed | yes | On a cursor-edge walk, page and offset caps are chunking controls. This is an enumeration rather than a cross-page snapshot; a custom service without cursor edges or a DISTINCT-without-PK shape has an explicit `MaxOffset` refusal boundary. |
| 11 | Remote | Entity routes intentionally narrow away list filters; the live blocker is that `remote.Resource` is not a `crud.Core`, so it cannot be wrapped in `security.Gate`. | blocker | yes | A calling service must rely on the far service’s tenant enforcement instead of declaring the same gate locally. |
| 12 | Codegen | `-out` can overwrite authored files and escape via traversal or symlink. | blocker | yes | A directive typo can destroy version-controlled application source. |
| 13 | Port + General (merged) | `Errors(...)` configures no generated route; General retains the README/all-transport blast radius. | blocker | yes | The documented catalogue installation silently leaves generated routes on default messages. |
| 14 | Port | Installing a custom renderer silently loses generated path hops. | blocker | yes | The consumer’s error body changes from its wire field back to a model field when localisation is added. |
| 15 | Query | Empty boolean/`notIn` groups, invalid unary values, and repeated scalars can silently widen, reverse, or alter a query. | serious | no | A malformed UI request receives valid data for a different question. |
| 16 | Vvdb | `params.host` can overwrite a named PostgreSQL socket host; replica declarations can open the primary twice or discard topology. | blocker | yes | A configuration can connect successfully to the wrong database or topology. |
| 17 | Utils | Spaced false booleans, unknown keys, second documents, and nested/optional inputs can be silently ignored or changed. | serious | yes | A reviewed deployment can boot with different safety settings than its file says. |
| 19 | Auth | An unavailable JWKS endpoint is rendered as bad credentials; withdrawn keys lack a bounded lifetime. | serious | yes | Cold deployments misreport an identity-provider outage and give revocation no predictable deadline. |
| 20 | Faults | `CodeOnly` can still reveal a driver-derived public field, and `Skip` can suppress unrelated constraint families sharing a name. | blocker | yes | Explicit privacy/oracle controls can silently fail after the caller selected them. |
| 21 | Errs | Empty codes, unknown kinds, empty paths and negative indexes can render as normal error bodies. | serious | yes | Clients receive syntactically valid responses for actions the service did not actually define. |
| 22 | Adapters | Cursor-close errors are discarded. | serious | yes | A database read can be reported as success after the cursor reports its only close-time failure. |
| 23 | Crud | A hand-written `In` filter has no documented bind budget, batching, or refusal. | serious | yes | An ordinary export/cleanup fails only after the public API accepted an adapter-sized statement. |
| 24 | Crudtest | `Normalize` erases meaningful quoted SQL whitespace and cancelled contexts are ignored by core fake operations. | serious | yes | A test suite can go green over wrong SQL or fake work a real source should refuse. |
| 25 | Authhttp + Crudgrpc (merged) | The documented gRPC stream auth wiring omits `StreamErrors` and returns `Unknown`. | serious | yes | A shipped copy-paste path returns the wrong wire status for authentication refusal. |
| 26 | Authhttp | Fiber has an independent refusal writer that drops repeated headers and has no parity control. | serious | yes | One binding produces a different security/error protocol from the other transports. |
| 27 | Codegen | Active build tags are ignored, named flags can silently omit models, and checked-in output has no read-only freshness check. | serious | yes | CI can accept a generated artefact that the target build cannot safely use. |

## Migration note — `crudsql` transaction options

The exported `crudsql.DB.TxOptions` field has been removed because retaining a
caller-owned `*sql.TxOptions` made a configured source mutable and race-prone.
Code that assigned the field directly must instead derive the source explicitly:

```go
db = db.WithTxOptions(&sql.TxOptions{
    Isolation: sql.LevelSerializable,
    ReadOnly:  true,
})
```

`WithTxOptions` snapshots the value; changing the input struct afterwards does
not reconfigure `db`. Passing nil selects the `database/sql` driver default.

## Migration note — `dbpgx` read/write options

`ConnectReadWrite` and `MustConnectReadWrite` now require scoped
`ReadWriteOption`s. Existing genuinely common hooks migrate explicitly:

```go
primary, replica, err := dbpgx.ConnectReadWrite(
    ctx, &cfg.DB, dbpgx.Common(tracing),
)
```

Credentials, IAM providers and role-changing hooks must instead use
`dbpgx.Primary(...)` or `dbpgx.Replica(...)`. This source-breaking declaration
prevents one option list from silently assigning the replica identity to the
writable pool.

## Migration note — sealed `auth.Option` and guard re-entry

`auth.Option` is now an opaque construction declaration instead of
`func(*auth.Guard)`. This closes the retained-option path that could mutate a
published Guard concurrently with requests. A custom helper that only wrapped a
ready-made option returns it directly:

```go
// Before: returned func(*auth.Guard) and invoked auth.Lookup inside it.
func PartnerHeader(name string) auth.Option {
    return auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
        key := get(name)
        return auth.Credential{Scheme: "ApiKey", Token: key}, key != ""
    })
}
```

A helper representing several declarations returns `[]auth.Option`, and the
caller expands it into `NewGuard`. `auth.Lookup` remains the low-level escape
hatch; no credential-source expressiveness moved behind an internal API. Nil
options remain no-ops.

Guard idempotence is now adjacent, not set-shaped. A -> A authenticates once and
A -> B runs both guards, leaving B's principal. A -> B -> A returns an internal
fault wrapping `auth.ErrAmbiguousGuardOrder`: without an assurance-order
declaration the framework cannot know whether B is a step-up or a downgrade.
Mount cumulative guards once each; model alternative credential kinds as one
`auth.Chain`. Every transport constructor also calls `Guard.Validate`, so replace
hand-built `new(auth.Guard)` values with `auth.NewGuard(...)`.

## DX, across the whole framework

The short zero-config paths are genuinely short: a repository can be defined,
mounted, queried, authenticated, or opened without application-owned framework
plumbing. The recurring cliff begins at the first real application rule. A
consumer must re-specify renderer/path-hop wiring per resource; must compose
security, query, faults, and adapters privately; and cannot safely extend
delete, remote identity, or generated-output checks from the short path.

The highest-return pre-tag changes are a single renderer-install seam that keeps
hops, a fail-closed declaration-validation posture for security/auth/config, and
the Query-owned page/export contract. [[D-060]] remains unresolved: Query must
select one page-cap authority and migration, including remote `GetAll` refusal;
`sqlrepo.MaxLimit` is input to that decision, not a competing third setting.
[[D-055]] is likewise unresolved: no `CredentialFrom` or remote forwarding API
exists until it decides credential placement, opt-in, lifetime, and all
transport boundaries.

## What this sweep could not settle

- Exact body-cap conformance is implemented but not pinned at exact/plus-one
  through all HTTP bindings; General and Crudhttp therefore do not claim it.
- SQLite in-memory, pgx overflow/cancellation, adapter callback/error identity,
  and several concurrent cache/option contracts remain local-control gaps rather
  than established defects.
- Schema resolution rejects malformed mappings when invoked, but no universal
  start-up pass invokes it for every mounted/defined model.
- The release roll-up deliberately does not promote ceremony alone (missing
  examples, convenience APIs, or documentation polish) to a tag blocker; those
  remain in the module sweeps.
