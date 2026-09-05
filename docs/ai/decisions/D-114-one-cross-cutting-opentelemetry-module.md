# D-114 — One cross-cutting OpenTelemetry module over base seams

**Status:** accepted
**Invariant:** Exactly one published module `otel/` (`github.com/frostgrove/vv/otel`, package `vvotel`) represents the consumer decision to use Frostgrove's OpenTelemetry integration. It adapts multiple dependency-neutral base seams (`port`, `storage`, `cache`). Non-OTel modules and the root remain OTel-free, and combination packages are forbidden.

## The decision

One module `otel/` with import path `github.com/frostgrove/vv/otel` and package name `vvotel` provides Frostgrove's OpenTelemetry decorators and adapters.

1. **One ecosystem decision:** A consumer choosing OpenTelemetry takes one module `vvotel`. The package provides typed middleware and observers for base seams (`port.Service`, `storage.Store`, `cache.Observer`, `cachememory.Observer`).
2. **Root stays clean:** The root module `github.com/frostgrove/vv` and other published modules never import `vvotel` or OpenTelemetry packages and expose no OTel types.
3. **No combination packages:** Packages such as `crudotel`, `storageotel`, `tenancyotel`, `storageminiootel` or `eventsourceotel` are not created. Telemetry composes linearly at base-owned seams at the application composition root.
4. **Lean API profile:** Production code in `otel/` imports only `go.opentelemetry.io/otel/trace` and `go.opentelemetry.io/otel/metric` (GA). It does not import SDKs, exporters, transport bridges or logging APIs. Providers are borrowed and never shut down by `vvotel`.

This decision amends:
- [[D-035]]: `vvotel` is accepted as the package name to avoid colliding with upstream `go.opentelemetry.io/otel`, where no single subsystem prefix applies.
- [[D-051]]: The single consumer decision is "use Frostgrove's OpenTelemetry integration across base seams", rather than splitting into per-subsystem OTel satellites.
- [[D-058]]: Top-level layout admits `otel/` as an explicit cross-cutting extension.
- [[D-074]]: Container bindings target their owning dependency-neutral seams without creating pairwise satellite combinations.

[[D-033]], [[D-036]] and [[D-048]] remain intact.
