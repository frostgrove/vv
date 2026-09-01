# Storage roadmap — current implementation and extension composition — 2026-09-01

**Status:** active revision. The dependency-neutral storage contract, filesystem
backend and MinIO backend are implemented. The typed extension chain, optional
storage decorators and the `storageminiofx` migration described here are not.
Public names for missing APIs remain illustrative until M0 accepts the common
extension ADR.

This revision supersedes the delivery and package sketches in the
[2026-08-26 storage snapshot](2026-08-26-1558-storage-roadmap.md). The older
document remains useful for key, streaming, staging, privacy and provider
conformance rationale; it no longer describes the current tree or extension
layout. The governing package and dependency rules are in the
[optional extension architecture roadmap](2026-09-01-extension-architecture-roadmap.md).

## Scope

Storage already provides one application-facing object contract over two
backend families. The remaining architecture work is deliberately smaller:

1. add one dependency-neutral composition point owned by `storage`;
2. let independently selected extensions decorate `storage.Store` without
   importing one another;
3. keep OTel, audit, tenancy, containers and concrete providers out of the base;
4. migrate the legacy MinIO × Fx combination to application-owned wiring; and
5. prove method, capability, stream-ownership and module-graph preservation.

This roadmap does not reopen the implemented key grammar, staging state machine,
error classes or backend choice merely to make extension wiring symmetrical.

## Current baseline

| Path | Current role | Dependency status |
|---|---|---|
| `github.com/frostgrove/vv/storage` | Logical namespace, validated keys, `Store`, `Backend`, portable types and error projection | Root module; standard library only |
| `github.com/frostgrove/vv/storage/storagefs` | Secure filesystem `Backend`, staging and application-served signed download links | Root module; standard library only |
| `github.com/frostgrove/vv/storage/storageminio` | MinIO SDK `Backend`, staging, bucket checks and pre-signed downloads | Optional MinIO module |
| `github.com/frostgrove/vv/storage/storageminio/storageminiofx` | Current MinIO-specific Fx constructors and lifecycle hook | Legacy pairwise module; migration required |

There is no `storages3`, `storageaws`, `storager2`, `storageotel`, `storageaudit`
or `tenantstorage` package. There is no storage middleware/chain yet and no
OpenTelemetry extension module yet.

The implemented `storage.Store` surface is:

```go
type Store interface {
    Put(context.Context, Key, io.Reader, PutOptions) (Info, error)
    Open(context.Context, Key) (io.ReadCloser, Info, error)
    Head(context.Context, Key) (Info, error)
    Delete(context.Context, Key) error

    Stage(context.Context, io.Reader, StageOptions) (Staged, error)
    Promote(context.Context, StageID, Key, PromoteOptions) (Info, error)
    Abort(context.Context, StageID) error
    CleanupExpired(context.Context, CleanupOptions) (CleanupResult, error)

    TemporaryURL(context.Context, Key, TemporaryURLOptions) (Link, error)
    Capabilities() Capabilities
}
```

Applications construct the implemented base contract explicitly:

```go
backend, err := storagefs.New(&storagefs.Config{
    Root: "/srv/example/objects",
})
if err != nil {
    return err
}

documents, err := storage.New(&storage.Config{
    Namespace: "documents",
    Backend:   backend,
})
```

`storage.New`, not the historical `storage.Open` sketch, scopes one injected
backend to a logical namespace. `TemporaryURL` is a common Store operation:
filesystem and MinIO implement it with different backend-owned mechanisms.

## Architectural decision

Storage owns the contract and the composition seam. Provider adapters implement
`storage.Backend`. Cross-cutting extensions wrap `storage.Store`. Neither shape
permits the base or a concrete provider to import an optional extension.

```text
                     application composition root
                                  |
                  constructs backend and Store once
                                  |
          audit middleware -> vvotel middleware -> storage.Store
                 |                    |                  |
                 +--------- imports root storage -------+

storage / storagefs ------------X------------> optional extensions
storageminio -------------------X------------> vvotel, audit, Fx bindings
extension A --------------------X------------> extension B
```

The target module layout is linear:

```text
storage/                         root module, OTel-free
  storagefs/                     root module, stdlib-only backend
  storageminio/                  one MinIO ecosystem module

otel/                            one OTel ecosystem module, package vvotel
audit/                           one future audit extension, if accepted
```

The target does not add a module for `storagefs`: standard-library code creates
no dependency choice to isolate. A future provider module is justified only by
a real independently selected SDK or provider contract, not by a marketing label
for an endpoint already served by the selected MinIO/S3-compatible client.

## Base-owned seams

### Backend implementation seam

`storage.Backend` remains the provider seam. `storagefs.Backend` and
`storageminio.Backend` implement it; `storage.New` validates and projects the
backend through one namespace. OTel and audit do not decorate concrete backends,
because that would couple logical semantics to one provider.

### Store middleware seam

M0 decides the exact API. The working shape follows the existing `crud` pattern:

```go
type Middleware func(Store) Store

func Chain(base Store, middleware ...Middleware) Store
```

The accepted form must have these properties:

- the first middleware listed is outermost;
- nil middleware is skipped;
- construction performs no global registration or component discovery;
- the result implements every current `Store` method;
- `Capabilities` is forwarded exactly and never inferred from a concrete type;
- a new Store method fails the method-inventory test until every built-in
  decorator records an observe/forward/refuse decision;
- an unknown wrapper is never skipped to execute an inner optional effect;
- context, error identity, caller-owned input readers and returned-body ownership
  are preserved exactly.

The chain is justified by at least two independent consumers: OpenTelemetry and
audit need the same ordinary Store wrapping shape. Tenant selection can also
produce a Store or middleware, but it does not justify a `tenantstorage` bridge.

### Capabilities and streams

`Capabilities` is part of `Store`, not metadata hidden behind an unwrap walk.
Every decorator returns the wrapped capability flags unchanged. A layer that
cannot preserve one refuses at construction; it does not hide the capability,
walk around itself or advertise a weaker value under the same Store contract.

`Open` completes when it returns the caller-owned `io.ReadCloser`; a decorator
must not silently redefine that call as the lifetime of later reads. `Put` and
`Stage` never close or replay the caller's reader. Instrumentation may observe
the method call but cannot buffer content, read it twice or wrap it merely to
inspect bytes.

## Illustrative linear composition

The missing names below describe the accepted shape; they are not implemented
APIs yet:

```go
base, err := storage.New(&storage.Config{
    Namespace: "documents",
    Backend:   backendForAuthorizedScope,
})
if err != nil {
    return err
}

documents := storage.Chain(
    base,
    audit.Store(auditor, auditStoreConfig),
    vvotel.Store(telemetry, telemetryStoreConfig),
)
```

The application owns `backendForAuthorizedScope`, chooses both extensions and
declares their order. The audit module and `vvotel` each import only root
`storage`; neither imports the other or `storageminio`. Removing `vvotel` leaves
storage results, errors and durable state unchanged. Removing audit removes its
durable evidence and, under a declared strict transactional policy, may change
whether the operation commits or returns an error. Those audit effects must be
explicit in the selected policy; neither extension silently redefines the base
Store contract outside its declared behaviour.

Tenancy follows the same direction. The application resolves and authorizes a
tenant before selecting a backend/Store, or supplies a tenancy-owned middleware
through the same base seam. A raw tenant identifier never becomes an implicit
namespace, key, bucket, telemetry label or fallback backend selection.

## Forbidden combinations

Do not create:

- `storageotel`, `storageminiootel`, `storagefsotel` or provider × telemetry
  packages;
- `storageaudit`, `auditstorage` or a durable-audit × provider package;
- `tenantstorage`, `storagetenancy` or one module per tenant topology;
- an i18n, event-source, jobs or cache bridge under `storage`;
- a package that bundles MinIO, Fx and another extension for a preselected graph;
- a generic `storage.Extension`, heterogeneous registry, service locator or
  reflection-based chain;
- build tags that hide optional dependencies inside the root module.

An extension may offer a `Store` middleware from its one public package. Files
inside that package may organize storage-specific implementation, but a public
subpackage/module is not created merely because the adapted seam is storage.

## `storageminiofx` migration

`storage/storageminio/storageminiofx` currently imports both the concrete MinIO
adapter and Fx. It therefore represents the intersection of two independently
selected choices. The common extension architecture narrows [[D-074]]: a
container adapter may target an owning neutral seam, but may not import a
concrete optional provider adapter.

The target is application-owned wiring around ordinary constructors:

1. application configuration constructs `*minio.Client`;
2. `storageminio.New` constructs the concrete backend;
3. the application exposes that value as `storage.Backend` to its chosen
   container, if it uses one;
4. bucket provisioning and Fx lifecycle hooks remain application policy; and
5. `storage.New` constructs each logical namespace after the backend is ready.

There is no replacement `storagefx`, `miniofx` or bundle module in this roadmap.
A generic Fx adapter may remain only when it binds a dependency-neutral owning
seam and imports no concrete optional provider.

Migration is compatibility work, not an immediate deletion:

- M0 records the exact deprecation/removal release and whether the current module
  has a supported consumer window;
- documentation first publishes one application-local hand-wiring example with
  equivalent startup refusal and bucket-check behaviour;
- `storageminiofx` receives no new cross-cutting integrations while deprecated;
- isolated fixtures prove hand wiring does not pull Fx into a non-Fx MinIO
  consumer and does not pull MinIO into another Fx consumer; and
- removal occurs only after the accepted compatibility gate, with the old module
  path named in release notes.

## Delivery plan

### M0 — freeze the storage extension contract

1. Accept the common extension ADR and its narrowing of [[D-074]].
2. Freeze `Middleware`/`Chain` naming, first-is-outermost order, nil behaviour,
   typed-nil handling and panic policy.
3. Record a method inventory for all ten Store methods and a capability matrix.
4. Record the `storageminiofx` deprecation and compatibility plan.
5. Freeze dependency allow-lists for root storage, MinIO and each extension
   fixture.

M0 closes only when two unrelated fake middleware can be expressed without an
extension-owned type in `storage` and without a combination package.

### M1 — implement and prove the base chain

1. Add only the accepted standard-library/first-party middleware and chain code
   to root `storage`.
2. Test deterministic order, nil handling and construction failures.
3. Drive all Store methods through two fake middleware in both relevant orders.
4. Prove exact `Capabilities`, error identity, cancellation and source/body
   ownership.
5. Make the method inventory fail when a Store verb is added without a decorator
   decision.

### M2 — compose independent extensions

1. Implement `vvotel.Store` only in the one OTel module under the OTel roadmap.
2. Implement an audit Store middleware only in the one audit extension if that
   roadmap accepts it.
3. Build an application fixture that composes base, audit-shaped fake and
   OTel-shaped decorator without either extension importing the other.
4. Run privacy fixtures over key, namespace, stage ID, metadata, ETag, version,
   provider error and temporary URL sentinels.

### M3 — migrate the legacy container combination

1. Publish the application-owned MinIO/Fx wiring example and startup tests.
2. Apply the accepted deprecation marker and release notes to `storageminiofx`.
3. Prove its replacement owns MinIO client, bucket check and lifecycle explicitly.
4. Remove the pairwise module only at the recorded compatibility boundary.
5. Re-run discovered-module, workspace and release checks after removal.

## Verification gates

| Area | Required evidence |
|---|---|
| Honest status | Implemented Store/backends and illustrative extension APIs are labelled separately |
| Base optionality | With `GOWORK=off`, root, `storagefs` and base-only fixtures contain no OTel, audit, Fx or MinIO graph they did not select |
| Provider isolation | `storageminio` imports root storage and the selected MinIO ecosystem, not OTel/audit/tenancy/container extensions |
| Dependency direction | Source/import checks reject base-to-extension and extension-to-extension imports |
| Chain order | First middleware is outermost; two fake extensions observe deterministic nesting in both relevant orders |
| Surface totality | Every Store method has an explicit decorator decision and additions fail inventory tests |
| Capabilities | Flags remain exact through every built-in order; unknown wrappers cannot reveal hidden effects |
| Resource ownership | `Put`/`Stage` readers and `Open` bodies retain the implemented ownership contract |
| Error/context | Cancellation, deadlines, wrapping and `errors.Is` classifications match the undecorated Store |
| Privacy | Keys, namespaces, stage IDs, URLs, credentials, metadata and provider text do not enter generic telemetry |
| Linear growth | Adding audit and OTel adds two modules/decisions and no storage-specific intersection package |
| Legacy migration | `storageminiofx` has an accepted deprecation window and an equivalent application-wiring fixture |
| Workspace/release | Intended modules equal workspace discovery, published modules have no `replace`, and tags remain lockstep |

## Definition of done

This revision is complete only when:

1. the implemented storage API and current module graph remain unchanged for a
   consumer that selects no extension;
2. the common ADR and storage chain contract are accepted;
3. two unrelated fake extensions and the OTel-shaped implementation compose
   through `storage.Chain` without reflection or a combination package;
4. every Store method, `Capabilities`, error and stream-ownership obligation has
   executable preservation evidence;
5. one `vvotel` module supplies storage telemetry and no storage/provider OTel
   module exists;
6. audit, tenancy and other optional behaviour enters only through root seams or
   explicit application construction;
7. isolated module graphs prove root/storage-only, MinIO-only, OTel-only and
   combined consumers download only what they selected;
8. privacy and cardinality corpora pass for filesystem and MinIO fixtures;
9. `storageminiofx` has an accepted migration/deprecation plan and equivalent
   application-wiring evidence rather than being silently grandfathered, with
   removal tied to the recorded compatibility boundary; and
10. current module docs and examples distinguish implemented constructors from
    illustrative extension APIs.

## Explicit non-goals

- changing the Store key grammar, staging model or error taxonomy for telemetry;
- adding `List`, recursive delete, automatic retry or provider raw options;
- making a Store authorize an actor or infer a tenant from context/baggage;
- treating an OTel span or audit record as storage correctness or durability;
- wrapping returned readers to claim a longer operation than the Store method;
- adding a provider package before an independently selected SDK and conformance
  need exists; or
- deleting a legacy module without the M0 compatibility decision.
