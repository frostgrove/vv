# FL-034 — A Frostgrove operation becomes OpenTelemetry signals

**Status:** binding map for OpenTelemetry roadmap slices O1–O3. Current and
owed behaviour are named separately below.
**Current entry point:** `vvotel.New` or `vvotel.Must`, followed by `vvotel.Service`,
`vvotel.Store`, `vvotel.Cache`, `vvotel.CacheMemory` or
`vvotel.CacheMemoryStats`; independent
`vvotel.TraceHandler` for stdlib log correlation
**Owed entry points:** every adapter explicitly marked **owed** below
**Implements:** [[UC-030]] · **Governed by:** [[D-134]] [[D-048]] [[D-061]]
[[D-062]] [[D-084]] [[D-091]] [[D-096]] [[D-118]] [[D-119]]

This flow is the complete ownership and call-lifecycle map. Every implemented
adapter terminates at a native OTel API; every owed adapter must do the same. SDK
providers, native wire/driver instrumentation, export and shutdown remain in
application code.

## Assembly and schema

1. **`internal/otelreg/{registry.json,history.go,signal_history.json,availability_history.json}` →
   `cmd/vv-otel-gen/{main.go,facts.go,inventory.go,ownership.go,shapes.go,variants.go}` →
   `otel/schema_gen.go` + `otel/wire_manifest.json`.** The current
   `vv-otel/v2` registry, generated schema and wire manifest contain 59 stable
   signal IDs with the source-inventory and contract validation defined by the
   schema specification. All 59 signals are marked implemented. The registry is
   the source of every component, operation, outcome, failure/reason,
   span/metric/attribute name, unit, bound, maturity and privacy/cardinality
   rule, component-owned span-name domain and exact declared-attribute source
   set; generated source and wire manifest are stale together or current
   together.
2. **`otel/telemetry.go:New` → `otel/assembly.go`.** `New` takes one descriptor
   snapshot, validates and copies caller-ordered `Config.Disable` before
   disabled/provider handling, unions exact legacy aliases, and builds a private
   active set. `Disabled` performs no provider/API work. Otherwise at least one
   provider is required; only selected provider families are obtained, tracer
   before meter. All enabled provider-compatible metrics are constructed in
   numeric signal-ID order with registry
   description, unit and boundaries. No callback is registered. Provider and
   instrument nil/error/panic failures return a redacted `*AssemblyError` and
   stop construction without lifecycle rollback. Panic values are discarded;
   only a returned constructor error is unwrapped.
3. **`otel/telemetry.go:Must`.** It calls `New` and panics only on its assembly
   error. It owns no Resource, global provider, goroutine, flush or shutdown.
4. `Telemetry` holds its immutable active set, borrowed tracer/meter, eagerly
   constructed instruments and the shared token state for one explicit cache
   memory aggregate registration. Value copies share that token state. Current
   adapters read their exact signal IDs independently; omitting an adapter
   removes that semantic layer. Native OTel providers and APIs remain directly
   usable by the application.

## Bounded-operation path

The current command and storage adapters use the six-step path below through
`otel/operation.go`; later operation adapters must reuse it. Storage duration
and size metrics remain B02 work.

`otel/safe.go`, `otel/service.go`, `otel/storage.go` and each owed operation
adapter must implement the completed path without creating a root framework
telemetry facade:

1. Map the operation through the generated closed registry. Invalid/unknown
   enum values never become arbitrary attributes.
2. Start the span in an isolated safe path. On success, retain its returned
   context. On trace failure, retain the incoming context and continue to the
   metric and wrapped operation.
3. Invoke the wrapped operation exactly once with the selected context. A
   working trace sees nested children; the same context is passed to metric
   recording so an eligible point can carry that span's exemplar.
4. Classify success, error, cancellation, timeout or panic through a closed
   mapping. Success leaves span status unset and omits `error.type`. An error
   sets Error status. No raw error is recorded.
5. Record each metric in its own safe path. A metric failure cannot suppress or
   alter the span or wrapped result; a trace mutation failure cannot suppress a
   metric.
6. End a started span once. On panic, record the bounded panic outcome and
   re-panic the original value. A return through `runtime.Goexit` is not labeled
   panic.

Every complete attribute set is admitted through the generated signal matcher
before `Start`, `SetAttributes`, `Record`, `Add` or `AddEvent`; admission returns
a fresh slice. IDs, payloads, SQL, bind values, keys, URLs, headers, cookies,
credentials, identity, error text and stack traces do not reach the OTel API. A
caller-approved `ApprovedName` is span-only. Dynamic strings require
`ApproveName`/`MustApproveName`; a direct conversion remains the low-level path
but cannot bypass runtime validation or the shared 32-name budget.

The closed source vocabularies are reconciled with `errs/code.go` and
`storage/errors.go`. Planned jobs mappings additionally consume only the typed
values defined by `jobs/delivery_command.go`, `jobs/disposition.go` and
`jobs/admission.go`; those values are never converted from an arbitrary label.

## Synchronous semantic boundaries

| Entry | Files | Current | Owed target |
|---|---|---|---|
| `vvotel.Service` | `otel/service.go:executeCommand`, `otel/operation.go` | One INTERNAL span and `vv.command.duration` per service command. The derived context reaches the inner service and histogram. Trace and metric faults are isolated; panic is rethrown unchanged; `runtime.Goexit` is span-only `goroutine_exit`. `Meta`, `Paths`, restore discovery and all ten effects are preserved. | Complete; later adapters reuse the recorder rather than adding a facade. |
| `vvotel.Store` | `storage/store.go`, `otel/storage.go:executeStorage`, `otel/operation.go` | One fail-safe INTERNAL span for each of the nine Store calls. `Open` ends when the reader is returned; `TemporaryURL` ends when the link is created. Panic/Goexit follow the shared terminal rules. There is no storage duration or result metric. | Add `vv.storage.operation.duration` for all nine operations. Planned signal ID 5, `storage_operation_bytes`, emits only `put/ok` from returned `Info.Size` and `stage/ok` from returned `Staged.Info.Size`; it never reads the source. Planned signal ID 58, `storage_cleanup_removed`, emits only a successful returned `CleanupResult.Removed` in `0..storage.MaxCleanupLimit`, with the exact returned `More`; invalid results, errors and panics emit nothing. Trace, duration and both result paths remain independent. |
| `vvotel.Store(..., vvotel.WithStorageStreams())` | `otel/storage.go`, `otel/storage_stream.go` | The returned `vvotel.StorageStream` implements `io.ReadCloser` plus `Unwrap() io.ReadCloser`, starts a separate stream span lazily at first Read/Close and ends once on EOF, non-EOF error, close, unwrap, panic or Goexit. It returns every underlying `(n, error)` unchanged and counts only valid `0 <= n <= len(p)` values with checked `int64` accumulation; the first invalid count or overflow suppresses only the byte sample. It retains only a captured SpanContext. | Re-run the hostile provider/nonconforming-reader matrix in O4; the production lifetime and concurrency contract is implemented. |
| `vvotel.Cache`, `vvotel.CacheMemory` | `cache/runtime.go`, `cache/cachememory/observer.go`, `otel/cache.go`, `otel/cachememory.go` | Facade/backend layers emit separate closed operation/outcome/reason/memoized attribute sets to the compatibility and event counters, optional active-span events, the shared items histogram and their encoded/payload or value/charged byte histograms. Schema admission drops impossible tuples and negative/absent measurements; cache names and keys are never attributes. | Complete; O4 re-runs hostile provider and observer edges. |
| `vvotel.CacheMemoryStats`, `vvotel.MustCacheMemoryStats` | `cache/cachememory/backend.go`, `otel/cache_stats.go` | `Backend.StatsContext(context.Context) (Stats, bool)` immediately returns unavailable for cancellation or mutex contention. One explicit registration accepts 1..64 unique backend pointers and uses only this bounded snapshot; it emits six enabled aggregate gauges, omits the whole aggregate when admission fails, and gates entries/bytes/limits on `Stats.Closed`. A Telemetry-shared token refuses a second registration, including through a value copy. The public `Registration { Unregister() error }` hides native OTel's embedded marker. Teardown is idempotent and concurrent-safe: deactivate → safe native unregister → in-flight drain → reference clear → token-checked singleton release. | Complete; the application still owns provider-wide uniqueness and SDK shutdown. |
| `vvotel.Authenticator` | `otel/auth.go` (**owed**) | Absent. | One INTERNAL span and `vv.authentication.duration` surround one call to the complete authenticator chain. The derived context reaches that chain. |
| `vvotel.Auth`, `vvotel.AuthEvents` | `otel/auth.go` (**owed**) | Absent. | `Auth` is counter-only: each terminal refusal increments unsampled `vv.auth.refusals`. `AuthEvents` is a separate event-only observer for the current span. Apply `auth.Sampled` only to `AuthEvents`, never to the counter. Both discard `Reason.Detail` and `Reason.Err`. |
| `vvotel.Health` | `health/registry.go`, `otel/health.go` (**owed**) | Absent. | Return a disabled Contribution unchanged; otherwise copy it and wrap only its Probe. One registry pass calls the original Probe once and records count/duration plus an optional INTERNAL span; metric collection calls no Probe. |
| `vvotel.Remote` | `otel/remote.go` (**owed**) | Absent. | One logical INTERNAL span and `vv.remote.duration` surround one `Transport.Do`. Application-owned client instrumentation emits the single wire CLIENT child. |
| `vvotel.Periodic` | `otel/runtime.go` (**owed**) | Absent. | One INTERNAL span and duration measure one callback invocation. It creates no timer/goroutine/name and re-panics unchanged so `runtime.Periodic` remains the containment owner. |

The `StorageStream` path treats only exact `err == io.EOF` as `eof`; a
wrapped EOF is the `error` terminal returned to the caller. Pre-start Unwrap and
an adapter with all three stream signals disabled preserve the raw reader and
emit nothing. The first start contender stores `startedAt` before Start and uses
only a background context carrying the captured SpanContext; the first terminal
stores `endedAt` under the state lock. Start/End use those explicit timestamps
and duration is `endedAt.Sub(startedAt)`, including when terminal wins while
Start is blocked. IDs 6, 7 and 8 are implemented with an appended availability
history.

## Direct Source calls and transactions

This path is **owed** and is admitted only after the executor walk exists.

1. `crud/executor.go:ExecutorUnwrapper` exposes
   `UnwrapExecutor() Executor` as one bounded navigation step. Source-semantic
   helpers use only `SourceUnwrapper`; executor-semantic helpers use only
   `ExecutorUnwrapper`. Each is an independent linear walk that inspects at most
   64 values including the outer value and invokes at most 63 unwrap transitions,
   not graph search. The 64th value is checked without calling its unwrap method.
   Nil/typed-nil targets, cycles, depth overflow and unknown wrappers fail closed.
   A divergent object implementing both
   interfaces follows the branch chosen by the helper, never type-switch order.
   `crud/adapter/crudsql/crudsql.go:Transaction` and
   `crud/adapter/crudpgx/crudpgx.go:Transaction` use the executor walk.
2. `otel/crud.go:Source` selects a concrete wrapper whose method set matches
   the inner source. Every wrapper implements `SourceUnwrapper` and
   `ExecutorUnwrapper`; neither walk executes an effect.
3. Direct Exec and Query each get one INTERNAL span and duration. Statement,
   arguments, table/columns, row values and result contents never travel. Query
   ends when the rows handle is returned.
4. Beginner and read-source capabilities are selected through the existing
   bounded `crud.BeginnerOf` and `crud.ReadSourceOf` walks. This preserves a
   declared `SourceUnwrapper` intermediary. The concrete outer `vvotel` wrapper
   performs the discovered Begin exactly once, instruments the returned
   transaction, and wraps the discovered replica before return. Nested Begin
   remains discoverable and no effect bypasses the outer recorder.
5. Source identity and transaction scope remain the inner source's exact
   answers. Both wrapper orders and a Source-only intermediary are covered.
6. `UnsafeBulkInserterOf` tests only the immediate outer source and never
   unwraps to grant the capability. If and only if that exact outer source
   implements `UnsafeBulkInserter`, the `vvotel` wrapper exposes it and starts
   the outer `unsafe_bulk` recorder before any native lookup. Only then may the
   implementation follow `ExecutorUnwrapper` to select the Tx/Conn used by
   native COPY. This navigation does not restore permission. Tests cover
   divergent dual-unwrappers, cycles/depth, source-only intermediaries, wrapped
   transactions, cross-datasource identity, unknown wrappers and exact-once
   COPY plus exact-once outer recording.

This is direct-call telemetry, not all-statements telemetry. Native driver
instrumentation remains responsible for database CLIENT spans, pool metrics and
row iteration.

## Runtime lifecycle and context-bearing logs

1. `runtime/observer.go:LifecycleObserver` and
   `runtime/observer.go:Observers`/`MustObservers` provide the bounded
   ordered fan-out preserves the old `Observer.Observed` state callbacks and the
   optional context-bearing lifecycle capability through a panicking sibling.
   `MaxObservers` is 8 and counts supplied arguments before nil/typed-nil
   filtering; overflow matches `ErrTooManyObservers`, while Must panics with the
   exact error. The fan-out implements both interfaces and forwards lifecycle
   events only to capable children.
2. `runtime/supervisor.go:Supervisor` emits the existing running/stopped/failed
   transitions unchanged, one terminal run event after its terminal state, and
   one lifecycle completion for every returning real Drainer. Expected nil or
   `context.Canceled` return during Stop is `run/ok`; unexpected nil becomes
   `ErrRunnerReturned`, and other returns use timeout/canceled/error precedence.
   Drain uses the exact call context/result and the same outcome precedence.
   Constructor-only idle state, readiness reads, nonreturn/Goexit and the current
   uncontained Drain panic emit nothing fabricated. **Owed.**
3. `runtime/runtimefx/runtimefx.go` uses the root fan-out so Fx composition does
   not erase `LifecycleObserver`. **Owed.**
4. `otel/runtime.go:Runtime` maps only completed run/drain data to metrics. It
   emits no process-lifetime span, runner name or error text. **Owed.**
   Existing `runtime.Observer.Observed` supplies transitions only;
   `RunnerState.Err` and timestamps never source completion counts/durations.
5. The structural inventory contains exactly eleven framework log call sites:
   six transport calls in `crud/http/crudnet/middleware.go`,
   `crud/http/crudnet/options.go`, `crud/http/crudgin/middleware.go`,
   `crud/http/crudgin/options.go`, `crud/http/crudfiber/middleware.go` and
   `crud/http/crudfiber/options.go`; one in `crud/rpc/crudgrpc/status.go`; two in
   `auth/http/authhttp/authhttp.go`; one in `jobs/classifier.go`; and one in
   `runtime/supervisor.go`. The first ten call a context-bearing slog method on
   `port/log.go:Logger(ctx)`. Supervisor keeps its injected application logger
   and passes the runner context at the call site, never a background context.
6. `otel/slog.go:TraceHandler` is current. `Handle` reads the current valid
   SpanContext and adds `trace_id`, `span_id`, `trace_flags` together. A reserved
   key already owned by the record, a group or prior `WithAttrs` suppresses the
   whole triplet. `Enabled`, groups and existing attributes pass unchanged.

`TraceHandler` correlates console/application logs. Application code chooses a
redaction handler and, if desired, an `otelslog` bridge; neither enters
`vvotel`.

`TestEveryContextBearingFrameworkLogPassesItsContext` structurally proves all
eleven sites, including Supervisor's runner context.
`TestTraceHandlerCorrelatesWithoutOverwritingCallerFields` behaviourally proves
the handler, its reserved-key collision rule and standard composition.

## Durable jobs

All paths in this section are **owed**.

### Carrier composition

1. `jobs/durable_context.go` adds `ContextCapture.Trace`,
   `ContextCapture.WithTrace`, `IdentityRestoreRequest.Trace` and
   `RestoredIdentity.WithContext`, plus root system provider/restorer building
   blocks. Each method returns or derives a validated copy and exposes no
   protected identity field. `WithContext` validates non-nil input, private
   lineage and the same Done channel/deadline; it does not claim to introspect
   arbitrary values or a future cancellation cause.
2. `otel/jobs_context.go:JobContext` calls the trusted provider once, injects
   only `traceparent`/`tracestate` with `propagation.TraceContext`, and replaces
   the capture's trace value only after admission. No valid current span leaves
   the provider trace unchanged. A valid span builds a fresh pair, clearing
   stale tracestate; if the full carrier exceeds the shared 1024-byte bound it
   retries traceparent-only, and if that also fails it returns the exact base
   capture. Correlations are never truncated. The outcomes are respectively
   absent, injected, tracestate_dropped and traceparent_dropped.
3. `otel/jobs_context.go:JobIdentity` calls the trusted restorer once, extracts
   a remote SpanContext and replaces only the restored context, using exactly
   `trace.ContextWithRemoteSpanContext(identity.Context(), spanContext)`. This
   construction, rather than the generic copy validator, preserves the trusted
   restorer's Done channel, deadline, delayed cancellation cause, other values
   and identity lineage. Invalid traceparent returns the exact base identity;
   invalid non-empty tracestate drops only tracestate and still attaches the
   valid remote parent. A failed `WithContext` copy returns the base identity.
   Each nil-error base call records exactly one closed propagation outcome; a
   base error or panic records none.
4. Durable validation continues to accept the pre-D-134 jobs grammar. If the
   OTel parser rejects legacy trace data, extraction records a closed propagation
   outcome and returns the successfully restored identity context without that
   invalid correlation. It never converts tracing into identity authority.

### Producer calls

`otel/jobs_enqueue.go` supplies four parity wrappers:

| Wrapper | Calls once | Span semantics |
|---|---|---|
| `vvotel.Enqueue` | `jobs.Enqueue` | PRODUCER; starts before context capture |
| `vvotel.EnqueueOnce` | `jobs.EnqueueOnce` | PRODUCER; preserves created/existing/conflict result |
| `vvotel.EnqueueIn` | `jobs.EnqueueIn` | INTERNAL; outcome is `staged`, never committed/published |
| `vvotel.EnqueueOnceIn` | `jobs.EnqueueOnceIn` | INTERNAL; preserves staged/dedup result and makes no commit claim |

Each wrapper forwards the exact queue, definition, stager, payload, intent and
options, and returns the exact ID/result/error. Metrics omit definition,
invocation, intent and partition labels.

### Handler invocations

1. `otel/jobs_handler.go:Job` delegates consumer construction to the ordinary
   typed jobs factory after wrapping its handler. Worker options remain sealed
   and unchanged.
2. `otel/jobs_handler.go:JobAdapter` wraps one `jobs.AdapterHandler`. It passes
   the exact payload, `DeliveryMeta` and same `AttemptController`; Pulse and
   Guard effects therefore reach the worker unchanged.
3. The default starts a new CONSUMER span linked to the extracted producer
   SpanContext. Parent-child is an explicit adapter option. It derives only the
   active-span context while preserving deadline, Done, cancellation cause and
   values.
4. Handler duration and eligible queue delay are associated with the
   `AdapterHandler` invocation and recorded with closed outcome and attempt
   positive ordinal bounded by `jobs.MaxAttemptOrdinal` (4129). A returned error
   describes only the callback. Worker
   timeout arbitration, retry/defer/permanent classification, backend Apply and
   control mutation remain outside.
5. The span ends when the handler returns. A timed-out handler that ignores
   cancellation retains an open span until its later return. Panic is recorded
   and re-panicked to the existing worker containment.

### Worker and scheduler control plane

1. `jobs/worker_observer_runtime.go` and `jobs/workers_run.go` must pass each
   real operation context to `WorkerObserver` rather than substitute
   `context.Background()` where one exists.
2. Before the OTel adapter, `jobs/worker_observer_runtime.go:observeApply` must
   preserve the root event's public "disposition and why" contract. When a
   `DeliveryCommand` has a nonzero `Disposition`, `WorkerEvent.Reason` is
   `Disposition.Reason()`; otherwise it is `DeliveryCommand.Reason()`. In
   particular, `finish_attempt` retains handler-failure, panic and other
   disposition reasons. The valid command/disposition/reason matrix is exact;
   this is a semantic correction, not workflow orchestration.
3. `jobs/worker_observer.go:WorkerObserver` supplies the bounded event and
   `otel/jobs_workers.go:Workers` maps every valid WorkerEvent to bounded counts,
   duration, items/bytes, admission and delivery mutation/control metrics.
   Every projection validates outer Operation/Outcome; items/bytes also consume
   Failure and delivery-result samples consume outer Results. Numeric ceilings
   are 1000 items, `jobs.MaxClaimBytes` (67108864) bytes,
   1..`jobs.MaxClaimItems` (256) result items and `jobs.MaxReclaimBatch` (1000)
   released leases.
   Definition, binding, payload and IDs are excluded from labels. Invalid or
   unknown events emit nothing; observer faults cannot stop work.
4. `jobs/schedule_observer.go` adds a context-bearing ScheduleObserver, bounded
   event and `ScheduleObservers`/`MustScheduleObservers` fan-out.
   `MaxScheduleObservers` is 8 and counts arguments before nil filtering;
   overflow matches `ErrTooLarge`, Must panics with the exact error and each
   child is ordered/panic-isolated. Nil receiver/context and failed cycle CAS are
   pre-admission and emit nothing. After a successful CAS, canceled context,
   clock failure, partial placement failure and success emit exactly one cycle
   event carrying the exact context/result/error and elapsed time. Result fields
   are each in 0..`jobs.MaxDefinitions` (4096). Manual and loop-driven cycles use
   the same `RunDue` path; there is no extra loop event.
5. `otel/jobs_scheduler.go:Scheduler` records cycle duration/outcome and the
   four result distributions. It emits no schedule/definition/intent/invocation
   name, due timestamp or polling span; it owns no ticker and derives no
   backlog.

A producer context may survive serialization and restart, but no span does. One
enqueue followed by a retry yields one producer span and two separately ended,
linked handler spans.

## Application-owned path to export

`_examples/otel-sdk-bootstrap/main.go` is the current application-owned setup;
the production/native recipes extend it in later roadmap slices. Application
code builds Resource, trace/metric providers, sampling, Views, processors,
readers and exporters; passes the same providers to native server/client/driver
instrumentation and `vvotel`; drains the application; then flushes and shuts
providers down with a deadline. `vvotel` participates in none of those lifecycle
steps.

`test/oteltelemetry/real_sdk_test.go` is the current isolated SDK proof. It
builds application-owned providers with `tracetest.SpanRecorder`,
`metric.ManualReader` and an always-on exemplar filter. One native SERVER span →
one Frostgrove command INTERNAL span → one native CLIENT span has exact
parentage; the command histogram exemplar carries that command span's trace and
span IDs. A second in-process gRPC receiver accepts actual OTLP trace and metric
exports without a Collector or external network. Both paths assert exact scope,
name, kind, status, end, attributes, histogram metadata/bounds and privacy.
Trace-only, meter-only, unsampled and global-provider isolation are separate
controls.

`make check-otel-live` runs this real-SDK and local-OTLP suite under the race
detector with `GOWORK=off`. It is an explicit local pre-release proof and is not
part of offline `make check`. Pinned Weaver semantic live validation remains
owed by OT-D05 because Weaver does not consume Frostgrove's JSON wire manifest.
Durable enqueue will separately prove PRODUCER → linked CONSUMER invocations.
The Collector remains a lossy operations pipeline, not an audit store.

## Verification

- `make check-otel-schema` and `make check-otel-module` for registry freshness
  and dependency isolation;
- `TestAssembly_RegistryRosterAndEagerConstructorOptionsAreExact`,
  `TestAssembly_EveryMetricConstructorFailureStopsAtTheSignal`,
  `TestAssembly_ExplicitDisableValidationPrecedesDisabledAndProviders` and
  `TestAssembly_EmittedSignalsDisableIndependently`
  for fail-fast construction, selection and availability semantics;
- `TestRealSDKPreservesParentsLinksExemplarsAndPrivacy`,
  `TestRealSDKErrorStatusAndAllowListReachExportBoundary`,
  `TestRealSDKSamplingDoesNotSuppressCommandMetric`,
  `TestRealSDKTraceOnlyMeterOnlyAndGlobalsRemainUntouched` and
  `TestLocalOTLPRoundTripPreservesFrostgroveSignalContract` for native SDK and
  serialized OTLP parentage, end, status, metrics, exemplars, sampling,
  provider isolation and privacy;
- exact-call/effect/capability tests for every decorator and callback;
- differential Trace Context and legacy-carrier tests across a process restart;
- a producer → retry → success fixture proving one producer and two linked
  handler spans;
- `TestStorageOperationBytesUsesSuccessfulReturnedSizesWithoutReadingSources`
  for the base signal ID 5 contract;
- cleanup-result tests for signal ID 58 prove exact returned counts at zero,
  one and `storage.MaxCleanupLimit`, both `More` values, no sample for an
  invalid result/error/panic and no extra `CleanupExpired` call;
- current `TestEveryContextBearingFrameworkLogPassesItsContext` for all eleven
  log sites, including Supervisor's runner context, and current
  `TestTraceHandlerCorrelatesWithoutOverwritingCallerFields` for handler
  behaviour;
- `TestWorkerObservationPreservesEachOperationContextAndEffectiveApplyReason`
  for the base root context/reason contract; exhaustive enumeration remains O4.
