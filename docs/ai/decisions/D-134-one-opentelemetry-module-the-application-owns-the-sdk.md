# D-134 — One OpenTelemetry module; the application owns the SDK

**Status:** in force from OpenTelemetry roadmap slices O1–O3; v2 registry and
manifest current
**Supersedes:** [[D-114]]
**Narrows:** [[D-062]] (logging only)
**Invariant:** `github.com/frostgrove/vv/otel` is the only Frostgrove
OpenTelemetry module. It borrows native providers and observes explicit,
dependency-neutral seams. The root and every other published framework module
remain OTel-free; the application owns SDK construction, native instrumentation,
export and shutdown.

## The decision

### One optional adapter module

The published module is `github.com/frostgrove/vv/otel`, package `vvotel`. It may
import the stable OpenTelemetry API packages needed for traces, metrics and W3C
Trace Context: `trace`, `metric`, `propagation`, `attribute` and `codes`. Its
stdlib log integration is a `slog.Handler` decorator. Production code in the
module may not import an OTel SDK, exporter, Collector client, contrib transport
instrumentation, `otelslog`, the OTel Logs API or a database-driver bridge.

Outside `vvotel`, the root module and every other published framework module
expose no OTel type and import no OTel package. Neutral seams remain owned by
their subsystems. There is no shared observability interface, no component
graph in `vvotel`, and no
`crudotel`, `jobsotel`, `eventsourceotel` or other pairwise package.

`vvotel` borrows caller-supplied `trace.TracerProvider` and
`metric.MeterProvider`. It starts no work, mutates no OTel global and acquires no
provider shutdown obligation. Assembly and signal-selection mechanics live in
[[FL-034]].

The current `vv-otel/v2` registry, generated schema and wire manifest contain
59 stable signal IDs. Six are marked implemented: command duration and span,
storage span, cache operation count, cache facade event and cache backend event.
The other 53 are explicitly planned and are not current runtime behaviour.
Fail-fast `vvotel.New`, `vvotel.Must` and the four existing adapters are current;
all remaining adapters are owed. Enabled provider-compatible metric descriptors
are constructed at assembly even while planned; availability governs emission,
not construction.

`New` validates and copies explicit `Config.Disable` IDs before disabled or
provider handling. It builds one private active set, obtains a tracer before a
meter when both selected families are present, and constructs enabled metric
descriptors in stable numeric signal-ID order. A trace-only or metric-only
configuration is valid and keeps context-only events active. `Disabled` performs
no provider/API work; an enabled configuration with neither provider is invalid.
No callback is registered during assembly.

Provider and instrument nil, panic and constructor errors become redacted typed
assembly errors. Panic objects are discarded. Only an error returned by an
instrument constructor is unwrapped. Construction stops at the failure; there
is no rollback because `vvotel` acquired no registration or lifecycle resource.
Legacy disable booleans are exact unions over their original semantic families,
and every current signal remains independently removable.

### Admitted seams and exact boundaries

| Frostgrove seam | `vvotel` boundary |
|---|---|
| `port.Service` | one INTERNAL command span and duration measurement around each command; metadata methods and optional restore capability are preserved |
| `storage.Store` through `storage.Middleware` | one current INTERNAL operation span; OT-B02 adds method duration, successful persisted size returned by Put or Stage and exact successful cleanup removals returned by CleanupExpired; OT-B03 adds optional stream observation starting at first read/close without buffering or retaining the request context, and an invalid Reader count or cumulative overflow suppresses only that stream's byte sample |
| `cache.Observer`, `cachememory.Observer`, owed `cachememory.Backend.StatsContext` | bounded event metrics and optional active-span events; one explicit aggregate memory callback registration accepts at most 64 unique backend pointers, uses only non-blocking context-aware snapshots and never creates one series per key or backend |
| `crud.Source` | direct Exec/Query; source semantics walk only `SourceUnwrapper`, executor semantics only `ExecutorUnwrapper`; native bulk permission exists only when directly exposed by the wrapped outer source, after which executor navigation may select its COPY transport target; Query ends when rows are returned, not when iteration ends |
| `remote.Transport` | one logical INTERNAL call; native client instrumentation owns the wire CLIENT span and propagation |
| `auth.Authenticator` and `auth.Observer` | one complete-chain authentication measurement and bounded refusal counts/events; no authorization-decision span is inferred |
| `health.Contribution` | a non-disabled contribution is copied exactly and only its existing Probe is measured; a disabled contribution is returned unchanged, and collection never calls a Probe |
| `runtime.Observer`, `runtime.LifecycleObserver` and a periodic pass callback | completed lifecycle/drain metrics and one bounded span per periodic pass; no process-lifetime span, timer or runner is added |
| `jobs.TrustedContextProvider` and `jobs.TrustedIdentityRestorer` | W3C Trace Context injection/extraction over validated copies; identity authority remains in `jobs` |
| `jobs.Enqueue`, `EnqueueOnce`, `EnqueueIn`, `EnqueueOnceIn` | four typed call-site conveniences call the corresponding root function exactly once; direct placement is PRODUCER, transaction staging is INTERNAL and only `staged` |
| `jobs.AdapterHandler` and the typed Consumer factory | one CONSUMER span per returned handler invocation, linked to the producer by default; it is not a delivery-attempt span |
| `jobs.WorkerObserver` and `jobs.ScheduleObserver` | bounded control-plane and scheduler-cycle metrics from their actual operation contexts; an apply event uses `Disposition.Reason()` whenever the command disposition is nonzero and otherwise uses the command reason; no polling span and no inferred backlog |
| `slog.Handler` | correlation fields from the current valid SpanContext; it does not export or redact logs |

The typed `vvotel.Job`, `vvotel.JobAdapter` and `vvotel.Periodic` callback
wrappers are admitted convenience functions, not new kernel middleware. The four
enqueue wrappers are admitted for parity with the four root enqueue functions;
using the root functions directly remains the uninstrumented low-level path.
Every adapter is independently removable.

The new one-method lifecycle, schedule and executor-navigation interfaces remain
subsystem-owned seams. They do not join the tier-zero contract manifest and do
not amend [[D-048]].

Runtime and scheduler observer composition each accepts at most eight supplied
children, counting before nil/typed-nil filtering, preserves input order and
isolates every child panic. The runtime fan-out implements the existing state
observer plus the optional lifecycle capability; it does not erase the latter.
Lifecycle observation begins only around a real Run/Drain invocation. Expected
Run cancellation during Stop is an `ok` completion; a scheduler cycle is
admitted only after its CAS succeeds, after which even canceled-context, clock
and partial-placement returns carry the exact result/error event. Pre-admission
conflicts fabricate no event. Exact declarations and truth tables live in
[[FL-034]].

### Source wrapping first requires an executor walk

`vvotel.Source` may ship only after the root has a bounded
`crud.ExecutorUnwrapper` navigation seam and transaction/native-effect discovery
honours it. Source-semantic helpers follow only `SourceUnwrapper`, while
executor-semantic helpers follow only `ExecutorUnwrapper`; both are independent
linear, fail-closed walks that inspect at most 64 values including the outer
value and invoke at most 63 unwrap transitions. The 64th value is checked, but
its unwrap method is not called. Nil/typed-nil targets, cycles, depth overflow
and unknown wrappers terminate discovery, and a value
implementing both seams follows the branch selected by the calling helper.
Navigation never grants permission to execute through a wrapper.
`UnsafeBulkInserterOf` therefore inspects only the exact outer source. Once that
source has already supplied the capability and the outer recorder has started,
executor navigation may locate the Tx/Conn used by native COPY; this selects a
transport target and does not restore permission. The decorator exposes only
capabilities the wrapped source actually has and preserves their effects
exactly. This extends [[D-061]] and keeps [[D-062]]'s
rule that complete database CLIENT and rows-lifetime instrumentation belongs to
the driver selected by the application. The concrete walk and wrapper matrix
live in [[FL-034]].

### Durable jobs keep identity and old records valid

The jobs root owns durable trace storage and the dependency-neutral immutable
access/copy seam needed to decorate its opaque capture and restored-identity
values. A copy may not expose or change tenant, actor, token, provenance or
epoch. `RestoredIdentity.WithContext` rejects nil, a changed private lineage,
Done channel or deadline; arbitrary Go context values and a future cancellation
cause are not introspectable and are not claimed as validations of this generic
seam. The built-in `vvotel.JobIdentity` provides the stronger behaviour by using
only `trace.ContextWithRemoteSpanContext` over the restored context, preserving
its other values and cancellation cause. The exact copy operations live in
[[FL-034]].

Newly injected carriers must satisfy the native OTel Trace Context parser before
they enter a durable record. The existing jobs carrier grammar is not tightened.
A record valid before D-134 remains restorable. When its legacy trace data is not
acceptable to the OTel parser, extraction drops only tracing or invalid
tracestate; it does not reject identity restoration or handler execution. The
built-in path propagates `traceparent` and `tracestate` only. Baggage is an
explicit application policy and never the default.

Injection with no valid current span preserves a provider-supplied trace. A
valid span replaces it with a fresh pair, so stale tracestate cannot survive; if
the shared carrier bound rejects that pair, tracestate is dropped before
traceparent, and failure of the traceparent-only candidate preserves the exact
base capture and every correlation. Extraction similarly drops an invalid
traceparent without changing the restored identity, while invalid tracestate
does not discard a valid remote parent. A successful base provider/restorer call
has exactly one closed propagation outcome; a base error or panic has none.

The producer span begins before context capture, so its context is what the
record carries. A handler invocation starts a new CONSUMER span linked to that
producer context by default. The wrapper sees only the handler body: decoding,
identity restoration, worker timeout arbitration, final disposition and backend
Apply remain outside it. Two handler invocations across a retry produce two
separate ended spans; one span never survives queue delay, retry sleep or process
restart.

### Runtime logging narrows D-062 without adding a logger owner

Request-scoped framework code continues to obtain the application logger through
`port.Logger(ctx)`. Long-lived runtime components that already receive an
application-owned `*slog.Logger` in their specification keep that field; they do
not replace it with `port.Logger` or add another logger option. Whenever either
path has a context, it calls a context-bearing slog method so a handler can read
the active SpanContext. This is the only logging amendment to [[D-062]]. No
framework code writes to a process-wide logger directly, and no binding gains a
logger option. The structural contract covers exactly eleven framework log call
sites and requires Supervisor to pass its runner context rather than a
background context.

`vvotel.TraceHandler` may correlate a record from its valid SpanContext but may
not replace caller-owned fields or change the wrapped handler's standard
behaviour. It exports and redacts nothing. The collision and grouping mechanics
live in [[FL-034]]; correlation is not sanitization.

### Signal and application ownership

All Frostgrove-created names, mappings, units, bounds, operations, outcomes,
failures and attribute sets come from the generated `vv-otel/v2` registry.
Values outside a closed mapping emit no arbitrary string. IDs, payloads, SQL,
keys, URLs, headers, cookies, credentials, tenant/user/job identity, error text
and stack traces never enter an attribute created by `vvotel`. A caller-approved
`ApprovedName` is trace-only and never a metric label. Dynamic strings require
explicit approval; direct low-level conversions remain runtime-validated.
Complete candidate sets pass the generated matcher before an emitting OTel API
call, including exemplar recording.

`storage_operation_bytes` keeps stable signal ID 5 and admits only the
`put/ok` and `stage/ok` variants. It records the persisted size reported by the
successful return: `Info.Size` from Put or `Staged.Info.Size` from Stage. The
adapter does not wrap, replace or read the supplied source to obtain it, emits
nothing for a non-successful call, and does not describe the value as bytes
actually consumed from the reader.

An adapter calls the wrapped business operation exactly once. Trace start,
trace mutation and metric recording are independent fail-safe paths. Failure in
one signal does not suppress another or alter a result. The context returned by
span start is passed to the wrapped operation and to metric recording. A panic
is classified, the span ends once, and the original value is re-panicked;
`runtime.Goexit` is not classified as panic.

The application owns Resource, deployment identity, SDK providers, sampling,
Views, exemplar policy, processors/readers, exporters, native HTTP/gRPC/database
and Go-runtime instrumentation, Collector deployment, flush and shutdown. The
same native providers may be passed to native instrumentation and `vvotel`.
Wire spans are emitted once by the native boundary; Frostgrove adapters emit only
the logical INTERNAL/PRODUCER/CONSUMER spans named above.

## What it forbids

- No hidden bootstrap, default provider, global setter, exporter, goroutine,
  retry, flush or shutdown hook in `vvotel`.
- No OTel import or OTel-typed seam in the root or another satellite.
- No generic `crud.Core`, `port.Repository` or cache-backend decorator whose
  method set can erase optional effects.
- No payload, identity, statement, key, URL, credential, arbitrary error or
  unapproved name in Frostgrove-created telemetry.
- No baggage by default and no trace carrier treated as trusted identity.
- No duplicate native HTTP, gRPC, database or storage-client span.
- No health collection that runs a Probe and no health importance chosen by an
  adapter.
- No process-lifetime runner or durable-job span, no span called delivery
  attempt, and no worker-local backlog estimate.
- No audit or exactly-once claim: loss of telemetry never changes a business
  result and telemetry is not the record of authority.
- No workflow history, signal interpreter, orchestration engine, replay engine
  or Temporal substitute.
- No profiles, eBPF or continuous-profiling wrapper without a Frostgrove
  semantic boundary; those remain native application tooling.
- No event-store, event-repository or projection adapter under this decision;
  those remain a separate, unimplemented event-sourcing roadmap item.

## Proven by (current and owed)

- `TestPublishedModulesOutsideOTelRemainOTelFree` and
  `TestVVOTelProductionImportsOnlyAllowedOTelAPIs` pin direct module boundaries
  and scan Frostgrove's own tagged `package main` files. `make check-deps` also
  walks the union of every satisfiable, importable, non-test production-package
  variant and rejects transitive OTel dependencies outside `otel/`. A
  dependency's standalone command/`package main` tool and a mixed-package tag
  set that cannot form an importable package are not import-graph edges; this
  uses no dependency or tag-name allow-list. Unused module requirements are not
  treated as package reachability. Unpublished tests and examples may import
  application-owned SDK tools.
- `TestSchema_WireManifestMatchesGeneratedDescriptors`,
  `TestSchema_AllSpanNameDomainsAreResolvedAndClosed`,
  `TestSchema_GeneratedMatchersFollowEveryMetricVariant` and
  `TestSchema_IntegerBoundsMatchAuthoritativeSourceLimits` pin the current
  registry inventory, closed mappings, units, bounds and forbidden metric
  attributes.
- `TestAssembly_RegistryRosterAndEagerConstructorOptionsAreExact`,
  `TestAssembly_EveryMetricConstructorFailureStopsAtTheSignal` and
  `TestAssembly_ProviderFailuresAreTypedRedactedAndOrdered` pin fail-fast
  selection, constructor order/options, provider precedence and redaction.
- `TestRealSDKPreservesParentsLinksExemplarsAndPrivacy` pins exported signal
  shape without process globals.
- `TestService_AllTenCommandsPreserveOneCallAcrossOutcomes`,
  `TestService_RestorableTotality`,
  `TestService_MetaAndPathsDoNotEmitSpans`,
  `TestStorage_AllNineOperationsTotality`,
  `TestStorage_CapabilitiesDoNotEmitSpan`,
  `TestCache_CounterRecordingAndSpanEvents` and
  `TestCacheMemory_CounterRecordingAndSpanEvents` pin current adapter effects
  and optional capabilities. `TestTelemetryFaultsNeverChangeBusinessResults`
  and `TestCacheAndCacheMemory_FaultsKeepSignalsIndependent` pin runtime
  failure isolation. `TestLegacyPanicNilModePreservesPanicAndGoexitSemantics`
  re-executes the current test binary with `GODEBUG=panicnil=1` and requires the
  service/storage panic(nil) and Goexit contracts to execute and pass.
- `TestJobContextInjectsTheCurrentW3CContextWithoutChangingTheCapture`,
  `TestJobIdentityExtractsARemoteParentAndPreservesIdentityContext` and
  `TestTraceCarrierRejectsDuplicatesUnallowedFieldsAndBounds` pin the base
  durable W3C contract; restart/retry proof remains OT-C07.
- `TestStorageOperationBytesUsesSuccessfulReturnedSizesWithoutReadingSources`
  pins the base signal ID 5 contract.
- `TestEveryContextBearingFrameworkLogPassesItsContext` pins all eleven log call
  sites, including Supervisor's runner context;
  `TestTraceHandlerCorrelatesWithoutOverwritingCallerFields` pins behavioural
  handler composition and reserved-field ownership.
- `TestWorkerObservationPreservesEachOperationContextAndEffectiveApplyReason`
  pins the base operation-context and disposition-reason contract; exhaustive
  enumeration remains OT-C05/O4.

## See also

[[D-021]] [[D-033]] [[D-048]] [[D-061]] [[D-062]] [[D-084]] [[D-091]]
[[D-096]] [[D-118]] [[D-119]] [[UC-030]] [[FL-034]]
