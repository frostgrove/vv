# Validation and publication choreography

The validation layers answer different questions:

1. `make check-otel-schema` proves generated Go and the Frostgrove
   `vv-otel/v2` wire manifest match the registry.
2. `make check-otel-module` proves a pre-release `GOWORK=off` consumer using
   explicit local replaces can compile without network access. It proves the
   checkout, not publication.
3. `make check-otel-operations` validates the operation artifacts and their
   metric references without a network or container runtime.
4. `make check-otel-live` exercises real isolated SDKs and an in-process OTLP
   receiver.
5. `OTEL_WIRE_INPUT=capture.json make check-otel-wire` validates an exhaustive
   OTLP protobuf-JSON capture against the generated `vv-otel/v2` signal names,
   scope version, span kinds, metric instrument kinds, number types and units,
   exact attribute sets, closed values, statuses, and dropped-attribute counts.
   Malformed `AnyValue` oneofs, trailing JSON, and log records emitted under the
   Frostgrove scope also fail. Set `VV_OTEL_WIRE_ALLOW_PARTIAL=1` only for a
   deliberately partial capture; extra signals or attributes still fail.
6. `make check-otel-collector` invokes the exact pinned Collector image's
   config validator. `make check-otel-prometheus` invokes the pinned `promtool`
   image and rule fixtures. Neither is part of offline `make check`.
7. `make check-otel-weaver` optionally starts the digest-pinned Weaver v0.24.2
   OTLP live-check listener against semantic conventions v1.40.0, waits for its
   health endpoint, and sends one explicitly selected native `otelhttp` GET
   fixture. The fixture uses literal `AlwaysSample`, explicit OTLP/gRPC
   exporters, and flushes and shuts down both providers. The check stops the
   listener, waits for a zero exit, and rejects an empty report, a missing HTTP
   server span or duration metric, and advice-level violations. Set
   `VV_OTEL_WEAVER_REPORT` to retain the validated JSON report. Weaver
   understands upstream semantic conventions; it does not validate
   Frostgrove's JSON registry or replace the Frostgrove wire checks.

Before tags, `make version V=$V` must rewrite every direct and indirect
first-party requirement in every published module to exact `$V` and remove all
checkout-local first-party replaces. Unpublished `test` and `_examples`
replaces remain. Release preflight checks the same invariant independently,
runs local/schema/live gates, and pushes the complete root and nested-module
tag set atomically. Only after the remote proxy can see the exact `otel/$V` tag
does the no-replace consumer run in a fresh workspace, `GOMODCACHE`, `GOCACHE`,
and `GOPATH`. A post-publication verifier can report a bad publication but
cannot roll back tags; remediation is a new version.

The Weaver target never sends Frostgrove custom spans to the upstream registry.
Run it only where Docker and registry network access are available; it remains
outside the offline default check.
