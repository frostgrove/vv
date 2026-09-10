# OpenTelemetry: maximal integration roadmap — 2026-09-08

**Status:** accepted baseline; O3 advanced capability pass passed; O4 edge and
vulnerability closure is active.
Fresh clean-context source-audit and delivery-order reviews passed. Every card
is an implementation commitment, not a list of possible ideas; existing
checkboxes describe only their stated current proof.

**Branch baseline:** `dev/ai-improvements` at
`c93886680a96502a67d126a20249466b5e07d3ab`.

**Supersedes:** the active status and remaining-work claims in
[the 2026-08-31 OTel roadmap](2026-08-31-opentelemetry-roadmap.md). That document
remains a historical record of the first `vvotel` release.

## Result to ship

One optional `github.com/frostgrove/vv/otel` module observes Frostgrove's
dependency-neutral seams. With both providers passed to `vvotel`, the default
enables every signal enumerated by the v2 registry and the accepted cards below.
Each adapter still works alone, every signal can be disabled, and callers retain
the native OTel API at every layer.

The application continues to own Resource, SDK providers, exporters, sampling,
Views, Collector deployment, flush and shutdown. `vvotel` borrows providers. It
does not create a framework bootstrap object, mutate OTel globals, import an
exporter/SDK/transport bridge, or make OTel a dependency of another module.

```go
tel := vvotel.Must(vvotel.Config{
	TracerProvider: traces,
	MeterProvider:  metrics,
})

service := vvotel.WrapService(tel, baseService)
store := storage.Chain(baseStore, vvotel.Store(tel, vvotel.WithStorageStreams()))
source := vvotel.Source(tel, databaseSource)
remoteTransport := vvotel.Remote(tel, nativeRemoteTransport)
authenticator = vvotel.Authenticator(tel, authenticator)
guard := auth.NewGuard(authenticator, auth.Observe(vvotel.Auth(tel)))

stats := vvotel.MustCacheMemoryStats(tel, memoryPrimary, memorySecondary)
defer stats.Unregister()

registry, err := health.Auto(
	vvotel.Health(tel, databaseCheck),
	vvotel.Health(tel, queueCheck),
)

supervisor, err := runtime.NewSupervisor(runtime.Spec{
	Runners: runners,
	Observer: runtime.MustObservers(vvotel.Runtime(tel), applicationObserver),
})

queue, err := jobs.NewQueue(jobs.QueueSpec{
	Context: vvotel.JobContext(tel, jobs.SystemContextProvider()),
})
consumer := vvotel.Job(tel, definition, handler, jobs.Concurrency(4))
workers, err := jobs.NewWorkers(jobs.WorkersSpec{
	Identity: vvotel.JobIdentity(tel, jobs.SystemIdentityRestorer()),
	Observer: jobs.MustWorkerObservers(vvotel.Workers(tel), applicationObserver),
}, consumer)

scheduler, err := jobs.NewScheduler(jobs.SchedulerSpec{
	Queue: queue,
	Observer: jobs.MustScheduleObservers(vvotel.Scheduler(tel), scheduleObserver),
}, schedules...)
cleanup, err := runtime.Every("cleanup", time.Minute, vvotel.Periodic(tel, clean))

logger := slog.New(vvotel.TraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
```

The snippet is target shape. Review may rename a symbol, but may not replace
the independent adapters with one universal bootstrap.

**Reflection policy:** Go reflection is an allowed implementation tool when it
removes caller boilerplate from a fact the runtime can determine exactly. The
current concrete use is typed-nil rejection at borrowed interface boundaries;
future reflected paths must have a closed input domain, a work bound, panic
containment and parity tests against explicit construction. Reflection never
discovers routes, application structs, payload/error fields, SQL, resource
identity or arbitrary plugin conventions. It also cannot manufacture a Go
method set, so exact CRUD optional-capability preservation still selects among
statically typed wrappers (generated code may remove internal repetition).

## Fixed ownership

| Concern | Owner | `vvotel` role |
|---|---|---|
| Resource and deployment identity | application SDK | none |
| Tracer/Meter providers | application SDK | borrow only |
| exporters, batching, retry, flush, shutdown | application SDK and Collector | recipes and tests only |
| HTTP, gRPC, database and remote-client wire spans | native instrumentation selected by application | document composition, never duplicate |
| Frostgrove command/storage/cache/auth/health/runtime/jobs semantics | `vvotel` | closed mapping and adapters |
| durable identity and trace-carrier storage | `jobs` | inject/extract through neutral accessors |
| attributes created by `vvotel` | `vvotel` before API calls | enforce privacy and cardinality before the SDK |
| caller-approved logical names and existing `slog` attributes | application | approve/redact before export; `vvotel` never claims to sanitize them |
| health importance and runner/job configuration | application | observe, never decide |

## Current defects this roadmap must close

| ID | Verified defect |
|---|---|
| B1 | command histograms record with the incoming context instead of the span context, so a newly created command span cannot become the exemplar |
| B2 | a span start or attribute panic disables an otherwise working metric for that call |
| B3 | lazy instrument creation suppresses an error or panic forever after one `sync.Once` attempt |
| B4 | fake providers never prove SDK parentage, links, exemplars or exported privacy |
| B5 | storage has spans but no operation latency metric; stream failures after `Open` are invisible |
| B6 | cache observers discard bounded reason/items/bytes/memoized data already present in events |
| B7 | framework `.Error` call sites with a context do not pass it to `slog.Handler` |
| B8 | durable jobs store a trace carrier, but decorators cannot preserve opaque capture/identity fields while adding or extracting trace context |
| B9 | worker events are dispatched with `context.Background()`, losing correlation and exemplars |
| B10 | jobs, auth, health and runtime have neutral observation seams but no `vvotel` adapters |
| B11 | the checked-in roadmap still says worker emission and event sourcing are absent |

## Hard exclusions

- No shared root observability interface. Each neutral seam remains owned by its
  subsystem.
- No `crudotel`, `jobsotel`, `eventsourceotel`, `otelx`, or other package grid.
- No Spring Boot-style OTel bootstrap, implicit global providers, hidden
  goroutine or hidden shutdown hook.
- No workflow engine, durable workflow history, signals, replay interpreter or
  Temporal clone.
- No audit guarantee. Export loss never changes a business result and telemetry
  is never the record of authority.
- No payload, SQL, storage key, URL, header, cookie, credential, tenant/user/job
  ID, error text or stack trace in an attribute created by `vvotel`. Existing
  application/framework `slog` attributes remain caller-owned and require an
  application redaction handler before log export.
- No baggage propagation by default. Trace Context is correlation, not trusted
  identity.
- No authoritative jobs backlog reconstructed from worker-local events. A
  backend snapshot seam does not currently exist.
- No generic `crud.Core`, `port.Repository` or cache-backend decorator. Their
  optional executable capabilities cannot be preserved by a casual wrapper;
  command/native-driver telemetry and the explicit adapters below remain the
  honest layers.
- No invented authorization-decision span. The current policy seam does not see
  the final Gate decision; applications observe it at the command boundary.
- No span named `delivery attempt`: a handler wrapper cannot see decode,
  restoration, timeout arbitration, final disposition or backend Apply.
- Event-store/repository/projection telemetry stays in the explicitly
  unimplemented appendices of the
  [event-sourcing roadmap](2026-09-01-postgres-event-sourcing-roadmap.md).
- OTel profiles, eBPF and continuous profiling get native application recipes
  when their selected tooling needs them; `vvotel` adds no wrapper with no
  Frostgrove semantic to contribute.

## A. Contract and failure model

### [x] OT-A01. Expand D-114 without weakening the microkernel

1. **Mechanism and source:** an OTel instrumentation library depends on API,
   receives providers, and leaves SDK configuration to the application, per the
   [OTel library guidelines](https://opentelemetry.io/docs/specs/otel/library-guidelines/).
2. **Why:** durable jobs need the standard propagation API, while transport,
   SDK and exporter ownership must remain outside Frostgrove.
3. **Adaptation:** write [[D-134]]. It keeps one published `otel/` module and all
   other modules OTel-free. It permits stable OTel API packages `trace`,
   `metric`, `propagation`, `attribute` and `codes` plus stdlib `slog` in
   production. It exhaustively names the allowed root seams and explicitly
   permits the four parity-tested
   jobs enqueue call-site wrappers and callback wrappers; they are conveniences,
   not kernel middleware. SDK, exporters, `otelhttp`, `otelgrpc`, `otelslog`,
   driver instrumentation and global setters remain forbidden. [[D-134]] also
   reconciles [[D-062]] with the existing application-owned logger fields on
   long-lived runtime components: those fields stay, but every call with a
   context must use a context-bearing slog method. [[D-114]] becomes superseded,
   not silently rewritten.
4. **Already present:** [[D-114]] fixes the one-module, borrowed-provider and
   no-combination-package rules, but its import whitelist and four-seam roster
   are both narrower than this roadmap.
5. **Top-level DX:** the application passes native providers once; every
   `vvotel` adapter remains an ordinary function.

**Implementation:** [[D-134]], decision index, expanded use case and flow map. The
decision lists `port.Service`, `storage.Store`, cache observers, `crud.Source`,
`remote.Transport`, auth, health, runtime and jobs seams individually and keeps
event sourcing in its own roadmap. Structural Go tests reject every production
import in Frostgrove's published non-OTel modules, including tagged
`package main` files, outside the exact `vvotel` API/seam allow-list.
`make check-deps` also rejects an OTel package anywhere in the union of every
satisfiable, importable, non-test production-package variant reachable from a
published non-OTel module. A dependency's standalone command/`package main`
tool and a mixed-package tag combination that cannot form an importable package
are not transitive import edges. Test files, testdata and unused module
requirements are outside the graph; no dependency or tag-name allow-list is
used.

**Done when:** dependency checks prove that the root and every non-OTel module
remain free of OTel, and `otel/` has no SDK/exporter/bridge dependency.

### [x] OT-A02. Make the registry the complete signal contract

1. **Mechanism and source:** a machine-readable semantic registry generates
   names and validates mappings; OTel Weaver applies the same registry-first
   model through `registry check`, `diff` and `live-check`.
2. **Why:** hand-written strings let spans, metrics, dashboards and docs drift
   independently.
3. **Adaptation:** move to `vv-otel/v2`. Add command, storage,
   `storage_stream`, cache facade/backend/memory, CRUD source, remote,
   authentication/refusal, health, runtime lifecycle/periodic, jobs enqueue,
   jobs propagation/enqueue/handler/worker/scheduler components. Store each
   component's operations, outcomes, reasons/failures, attributes, metric
   type/unit/bounds, source, maturity, privacy class and calculated cardinality.
   Generator validation is generic; it must not special-case cache. Generate a
   Frostgrove wire manifest for emitted-signal validation and record a v1 → v2
   migration.
4. **Current implementation:** `vv-otel/v2` is checked in as the registry,
   generated Go schema and wire manifest. It contains 18 components, 59
   stable integer signal IDs and 45 metrics. Six signals are marked `implemented` — command
   duration/span, storage span, cache operation count, cache facade event and
   cache backend event — while 53 are marked `planned`. Availability is part of
   the generated contract, so a planned descriptor is not a claim of runtime
   emission. An independent availability history rejects any unrecorded flip
   and permits only an appended `planned` → `implemented` promotion. Integer
   bounds remain exact through registry decoding, manifest
   generation and typed admission; compatibility float fields are not used to
   decide `int64` acceptance. Components explicitly own their span-name domains;
   declared attributes carry the exact AST-checked construction-source set and
   runtime name admission uses generated byte/charset authority. Full migration
   metadata is present in both generated surfaces.
5. **Top-level DX:** users refer to stable exported constants only when dropping
   to native OTel; normal adapters need no string names.

**Implementation:** registry, generator, generated source, wire manifest,
schema tests, `docs/modules/otel-spec.md` and v1 → v2 migration metadata are
current. Fail-fast assembly and adapters for planned signals remain in their
later cards.

**Done proof:** `TestRegistryRejectsInvalidContracts`,
`TestRegistryDecodingIsStrict`,
`TestGoAndManifestGenerationAreDeterministicAndCurrent`,
`TestSourceToWireAndFutureMappings` and `make check-otel-schema` reject
incomplete/duplicate/non-total mappings, invalid units/bounds, forbidden metric
attributes and stale generated output.

### [x] OT-A03. Fail fast when instruments cannot be constructed

1. **Mechanism and source:** OTel instrument constructors return an instrument
   and error; construction errors belong at application assembly, not on the
   first request.
2. **Why:** the previous `sync.Once` path permanently turned a failed instrument
   into a silent no-op.
3. **Adaptation:** `New` eagerly constructs every enabled synchronous and
   observable metric instrument when a MeterProvider exists. A constructor
   error, nil instrument or provider panic returns a typed error naming only the
   safe registry instrument. `Config.Disabled` remains the master no-op.
   `Config.Disable` is `Signals []Signal`, using explicit `Signal uint16` IDs,
   not a bitmask. Empty means all provider-compatible signals. `New` copies the
   caller slice and rejects unknown/duplicate explicit IDs before disabled or
   provider handling, then normalizes a private set; legacy aliases union
   idempotently after that validation. `AllSignals()` returns a fresh copy.
   `Must(Config)`
   is the magic-first constructor. Backend-specific callback registration is a
   later explicit fallible step under OT-B04. No constructor starts work or owns
   provider shutdown.
4. **Already present:** provider getter panic containment, nil-interface
   detection and per-component disable booleans.
5. **Top-level DX:** `tel := vvotel.Must(vvotel.Config{TracerProvider: tp,
   MeterProvider: mp})`; narrow deployments set `Disable` explicitly.

**Current implementation:** `otel/assembly.go` owns the copied private signal
set and typed redacted assembly failures. `New` obtains tracer before meter,
constructs the 45 enabled metric descriptors in numeric signal-ID order and
stores four typed instrument maps; it registers no callbacks. Planned
availability suppresses emission, not construction. Existing adapters select
IDs 1, 2, 4 and 9–11 independently. Legacy aliases retain their exact unions.

**Done when:** each enabled instrument/provider error, panic or nil and every
unknown or duplicate explicit disable ID is caught at `New`; trace-only, metric-only and fully
disabled instances all work. Registration failures are tested at registration,
not falsely attributed to `New`.

**Done proof:** `TestAssembly_RegistryRosterAndEagerConstructorOptionsAreExact`
pins 59/45/11/3 descriptors, 14/13/12/6 constructors, order and options;
`TestAssembly_EveryMetricConstructorFailureStopsAtTheSignal` injects
error/panic/nil into every metric descriptor; provider, selection, alias,
copying, `Must`, current-signal isolation and race tests cover the remaining
branches. OTel unit/race/vet and schema/module/dependency gates pass.

### [x] OT-A04. Isolate signals from each other and from business execution

1. **Mechanism and source:** the OTel error-handling contract treats telemetry
   as non-critical; an instrumentation fault must not fail the instrumented
   operation.
2. **Why:** today a failed span start/attribute write skips a valid histogram.
3. **Adaptation:** trace start, trace mutation and metric recording have
   independent safe paths. A bad trace falls back to the incoming context while
   the metric still records. A bad metric never suppresses the span. Panic
   handling records bounded `panic`, ends once and re-panics the original value.
   Cancellation and timeout retain their own outcomes. `runtime.Goexit` is not
   misreported as panic.
4. **Already present:** safe wrappers around OTel calls and panic-path tests.
5. **Top-level DX:** no recovery option; fail-safe runtime behaviour is the
   default. Native OTel remains available for callers wanting different policy.

**Implementation:** one shared internal operation recorder used by adapters,
without introducing a root framework telemetry facade.

**O1 scope:** fault-injection covers the provider/span/instrument methods
reachable through the current command, storage and cache adapters. Later cards
must extend the method inventory and fault matrix as they add OTel calls; this
checkbox does not claim coverage of unimplemented adapters.

**Current implementation:** `otel/operation.go` is the shared internal recorder
used by command and storage adapters. It keeps incoming and derived contexts,
span mutation and metric recording independent. Business errors and panic
values retain identity; a hostile classifier falls back to `internal`.
`runtime.Goexit` ends a started span once as `goroutine_exit` and emits no
incomplete-operation metric. A two-level invocation boundary distinguishes it
from business `panic(nil)` even under the legacy `GODEBUG=panicnil=1` runtime
mode, then re-panics the original value.

Every later adapter that co-emits a span and metric must pass the derived span
context to both the business callback and metric recorder. Its card owes a real
SDK exemplar assertion plus a trace-failure control that still records the
metric on the incoming context.

**Done proof:** `TestTelemetryFaultsNeverChangeBusinessResults` injects Start,
typed-nil span/context, SetAttributes, SetStatus, End and Record faults through
both service and storage shared-recorder wiring while pinning one business call.
`TestCacheAndCacheMemory_FaultsKeepSignalsIndependent` covers IsRecording,
AddEvent and Add faults. `TestPanicNilWithNilRecoverPreservesPanicAndGoexitSemantics`
re-executes the current test binary with `GODEBUG=panicnil=1` and requires all
four service/storage panic(nil) and Goexit contracts to run and pass. The fake
suite pins fallback metric context under trace failure; the real SDK suite pins
normal span/metric coexistence and exemplar correlation at the export boundary.

### [x] OT-A05. Enforce privacy and cardinality before the SDK

1. **Mechanism and source:** OTel's
   [sensitive-data guidance](https://opentelemetry.io/docs/security/handling-sensitive-data/)
   and SDK cardinality limits are secondary gates; Views may leave removed
   attributes in exemplar `FilteredAttributes`.
2. **Why:** filtering only in a View or Collector is too late for SDK memory,
   exemplars and alternate exporters.
3. **Adaptation:** every attribute created by `vvotel` comes from a closed enum.
   Optional logical names require an explicit caller-approved bounded-name
   value; they are omitted by default and never become metric labels. The type
   is an approval boundary, not automatic secret detection. Allow-listing
   happens before `Start`, `Record`, `Add` or `Observe`. Definition, binding,
   runner, check and resource names stay absent unless explicitly approved.
   Unknown enum values are omitted, never rendered as arbitrary strings. Trace
   IDs appear only as log correlation fields, never metric labels. `vvotel`
   preserves existing slog records and therefore does not claim to redact them.
4. **Already present:** closed command/storage/cache mapping,
   `MaxResourceNameValues=32`, safe error-code allow-list and no raw error
   recording.
5. **Top-level DX:** safe adapters need no names or redaction callbacks. A caller
   deliberately constructs an approved bounded name or uses native OTel when a
   deployment-specific dimension is worth its privacy/cardinality cost.

**Implementation:** central attribute builders inside `vvotel`, approved-name
type, schema bounds, forbidden-substring and high-cardinality tests across
spans, metrics and exemplars. The logging recipe separately composes an
application redaction handler.

**Done when:** tests inject secrets through IDs, keys, payloads, errors and
unknown enums and find none in telemetry attributes created by `vvotel`;
unapproved free-form names are absent. No test claims that a correlation handler
sanitizes caller-owned log records.

**Current implementation:** `otel/attributes.go` routes command, storage and
cache candidate sets through the generated exact matcher and returns a fresh
slice before any emitting API call. `ApprovedName` is the explicit boundary for
all logical resource declarations; literals remain concise, dynamic strings use
`ApproveName`/`MustApproveName`, and a direct conversion is revalidated at
runtime. One pointer-owned, mutex-protected 32-name budget spans service and
storage, survives public `Telemetry` value copies, does not charge duplicates,
and does not pre-reserve an unused configured default.

**Done proof:** generated-matcher mutation tests reject foreign values, extra or
duplicate keys and metric error codes; approval fuzz/boundary tests match the
generated charset/byte rule. Concurrent mixed-adapter tests pin the shared
distinct-name budget. Fake, real-SDK and serialized OTLP canaries recursively
check spans, events, metric attributes and exemplar filtered attributes.

### [x] OT-A06. Test the real OTel SDK, not only fakes

1. **Mechanism and source:** OTel Go provides `tracetest.SpanRecorder`, a metric
   `ManualReader`, `metricdatatest`, exemplar data and in-memory exporters.
2. **Why:** fakes with empty `SpanContext` cannot prove parentage, links,
   sampling or exemplars.
3. **Adaptation:** keep SDK imports in the unpublished `test/` module. Build
   isolated providers per test; assert span kind/name/parent/link/status/end,
   histogram sums/attributes/exemplars and no cross-test globals. Add a local
   OTLP round-trip fixture; Weaver remains a pinned optional live gate.
4. **Already present:** `otel/` fake-provider tests at high statement coverage
   and an unpublished SDK bootstrap example.
5. **Top-level DX:** application tests can copy one small fixture and inspect
   native SDK data without a Collector.

**Current implementation:** `test/oteltelemetry/real_sdk_test.go` uses isolated
real providers, `tracetest.SpanRecorder`, `metric.ManualReader`, an always-on
exemplar filter and official OTLP trace/metric exporters talking to an
in-process gRPC receiver. `make check-otel-live` runs the suite with
`GOWORK=off`, `-race` and no external Collector/network; it is not part of
offline `make check`. Pinned Weaver listener/send/report remains OT-D05 work.

**Done proof:** the suite asserts exact command span scope/name/kind/parent/no
links/status/end/attributes, native child parentage, histogram metadata,
boundaries, datapoint attributes and exact command exemplar IDs at both SDK and
serialized OTLP boundaries. It separately proves error privacy, NeverSample,
trace-only, meter-only and no global provider mutation. Isolated mutation runs
failed when derived context, metric recording, span end or a required
allow-listed attribute was removed, and when a payload secret was injected.

## B. Existing and neutral subsystem seams

### [ ] OT-B01. Correct command spans, metrics and exemplars

1. **Mechanism and source:** OTel metric exemplars are selected from the context
   passed to `Record`; nested operations must receive the context returned by
   `Tracer.Start`.
2. **Why:** command latency is the primary service SLI, and a slow histogram
   point should lead to its exact trace.
3. **Adaptation:** retain one INTERNAL span and one duration histogram per
   command. Record the metric with the derived command context. Span failure
   does not remove the metric. Preserve optional restore discovery and every
   `port.Service` effect exactly once.
4. **Already present:** all ten command verbs, closed outcomes/error types,
   `vv.error.code`, panic handling and explicit histogram boundaries.
5. **Top-level DX:** `vvotel.WrapService(tel, base, options...)` takes the typed
   service as an argument so Go infers `M`, `ID` and `U`. The composable
   low-level form remains
   `port.ChainService(base, vvotel.Service[M, ID, U](tel, options...))`.
   Reflection cannot infer generic result types or manufacture this method set;
   the direct helper applies that exact typed middleware once.

**Implementation:** service recorder refactor, the inference-friendly direct
wrapper, plus real-SDK parent/exemplar and capability-preservation tests.

**Done when:** each verb has success/error/cancel/timeout/panic coverage and a
real exemplar carries the command span's trace/span IDs.

**Current implementation:** `executeCommand` delegates to the shared operation
recorder and passes the context returned by `Tracer.Start` to both the wrapped
service and `Histogram.Record`. Trace failure falls back to the incoming
context without disabling the histogram. All ten command effects and optional
restore discovery remain unchanged. `WrapService` is still owed; the card is
reopened only for that DX surface and its exact-delegation tests.

**Done proof:** `TestService_AllTenCommandsPreserveOneCallAcrossOutcomes` pins
every command's full input, distinctive result and exact-one call across
success, error, cancellation, deadline and pointer-identity panic terminals;
`TestService_RestorableTotality` separately pins discovery, commands, results
and calls for Restore and RestoreMany.
`TestPanicNilWithNilRecoverPreservesPanicAndGoexitSemantics` executes panic(nil)
when recover returns nil and covers Goexit. The isolated SDK and OTLP tests assert SERVER
→ command INTERNAL → CLIENT parentage and an exemplar carrying the command
span's exact trace/span IDs.

### [ ] OT-B02. Add storage latency, persisted-size and cleanup-result metrics

1. **Mechanism and source:** logical library spans and client/network spans are
   separate OTel layers; a duration histogram exposes count and latency, while
   the successful Store return is the authoritative report of persisted size.
2. **Why:** storage currently traces sampled calls only, so operators cannot
   build an unsampled availability/latency SLI.
3. **Adaptation:** all nine Store calls record `vv.storage.operation.duration`
   using operation/outcome/error type. Stable signal ID 5,
   `storage_operation_bytes`, has only `put/ok` and `stage/ok` variants. On a
   successful Put it records the returned `Info.Size`; on a successful Stage it
   records the returned `Staged.Info.Size`. A negative successful size is outside
   signal 5's `0..MaxInt64` domain and suppresses only that result sample; it does
   not rewrite the result or suppress duration/tracing. It emits no result sample
   for error, cancellation, timeout or panic. The adapter never wraps, replaces
   or reads the supplied source to calculate the value: this is persisted size
   reported by the Store, not bytes actually consumed from the reader. `Open`
   measures only time to return the stream. Native S3/HTTP instrumentation owns
   network spans. `TemporaryURL` measures link creation, not later download.
   Stable signal ID 58, `storage_cleanup_removed`, records exactly the
   successful `CleanupExpired` return's `CleanupResult.Removed`, with its
   returned boolean `More` as `more`. The admitted count is
   `0..storage.MaxCleanupLimit`, inclusive. Error, panic or an out-of-range
   count emits no cleanup-result sample; the adapter never estimates removals
   from the requested limit or issues another cleanup. Capabilities remain exact.
4. **Already present:** nine INTERNAL spans, storage classifier and a typed
   `storage.Middleware`/`storage.Chain` seam.
5. **Top-level DX:** `storage.Chain(base, vvotel.Store(tel))`; trace-only or
   metric-only operation remains available through signal selection.

**Implementation:** add duration, successful persisted-size signal 5 and
successful cleanup-result signal 58 to independent shared-outcome recorders.
Put, Stage and CleanupExpired pass only their returned metadata into the
corresponding recorder; no source read or extra Store call is introduced. Each
result metric works with duration and tracing disabled. In the same change,
atomically promote IDs 3, 5 and 58 from `planned` to `implemented`, append each
transition to `availability_history.json`, reconcile current source inventory,
and regenerate Go schema and wire manifest. None may emit while its descriptor
remains planned.

**Base proof:** `TestStorageOperationBytesUsesSuccessfulReturnedSizesWithoutReadingSources`
distinguishes Put `Info.Size` from Stage `Staged.Info.Size` without reading or
replacing the source. Cleanup-result base tests pin the bounded returned result.
The exhaustive error, panic and numeric-bound matrix remains O4.
Duration proof covers all nine methods and success, error, cancellation,
timeout and pointer-identity panic terminals; a separate Goexit proof asserts
the required span-only terminal and no incomplete duration. Metric-only
operation, derived-context exemplars, trace failure fallback and a panicking
duration instrument are independent.

### [ ] OT-B03. Observe storage stream lifetime without changing streaming

1. **Mechanism and source:** OTel manual spans can cover an asynchronous
   lifecycle whose method returned earlier; byte metrics describe actual I/O,
   not declared object size. OTel Go's HTTP request-body path demonstrates
   preserving nil identity; its response wrapper does not preserve nil or
   typed-nil and is not a behavioural contract for Frostgrove.
2. **Why:** a successful `Open` followed by a read error is currently reported
   as success only.
3. **Adaptation:** `WithStorageStreams()` wraps only a successful, non-nil,
   non-typed-nil `io.ReadCloser`. Any Open error returns the exact original
   reader, Info and error without stream telemetry, even when the reader is
   usable. Successful nil/typed-nil readers also retain identity and nilness.
   If all three stream signals are disabled, the option returns every successful
   reader unchanged and performs no stream work.
   The separate `vv.storage stream` span starts lazily on first `Read` or
   `Close`, using only a captured SpanContext: the derived Open span context
   when Start succeeded, otherwise the incoming valid SpanContext. It retains
   no request context: Start receives only
   `trace.ContextWithSpanContext(context.Background(), captured)`. It ends
   exactly once on exact `err == io.EOF`, non-EOF error, close, unwrap or panic
   and counts bytes actually returned, including `n > 0` with an error. An error
   wrapping `io.EOF` is an `error` terminal, matching what ordinary `io` callers
   observe; the original error is still returned.
   The returned `vvotel.StorageStream` implements `io.ReadCloser` plus explicit
   `Unwrap() io.ReadCloser`. It never
   buffers, pre-reads, retries or changes cancellation. A close before EOF is
   `closed`; a close error is `error`. `Unwrap` before start returns the raw
   reader and completely disables observation without a span, duration or byte
   sample; after start it first ends with `unwrapped`.
   Every underlying `(n, error)` is returned unchanged. Byte accounting admits
   only `0 <= n <= len(p)` and uses a checked cumulative add bounded by
   `math.MaxInt64`. The first invalid count or overflow permanently invalidates
   only that stream's byte aggregate: `vv.storage.stream.bytes` is omitted,
   while terminal outcome, duration and span still complete normally.
   A short recorder-state lock orders completed observations only; it is never
   held across underlying Read/Close or an OTel call. Every Read/Close invokes
   the original method exactly once, including calls after termination or
   Unwrap; the adapter adds no serialization of the reader. The first terminal
   completion admitted under that lock wins. Its byte total includes earlier
   admitted reads and its own `n`; reads completing later return unchanged but
   cannot amend the ended metric. Unwrap returns immediately, never closes or
   waits for an in-flight read, and disables further observation. Concurrent
   first use starts at most one span; termination during Start records a
   pending end which closes the returned span once. The start winner records
   `startedAt` before calling `Tracer.Start`; the terminal winner records
   `endedAt` under the state lock. Outside the lock, Start receives
   `trace.WithTimestamp(startedAt)`, End receives
   `trace.WithTimestamp(endedAt)`, and duration is exactly
   `endedAt.Sub(startedAt)`, so a slow Start release cannot inflate lifecycle
   time. Underlying reader safety
   remains the reader's contract. A business Goexit ends an already-started span
   as `goroutine_exit` without an incomplete duration/byte sample; original panic
   values, including legacy `panic(nil)`, are rethrown unchanged.
4. **Already present:** `Open` explicitly ends its method span when the reader
   is returned; Store promises only `io.ReadCloser`.
5. **Top-level DX:** add `vvotel.WithStorageStreams()`; callers needing the raw
   reader omit it or call `Unwrap` deliberately.

**Implementation:** stream wrapper, duration/bytes metrics and deterministic
Read/Close/Unwrap race tests, with barriers around start, underlying effects and
completion. Test the full Open error/nil/typed-nil/success matrix, parent capture
with tracing enabled/disabled/start failure, EOF/error/close/unwrap/panic and
late-byte exclusion. Cover negative counts, counts larger than the supplied
buffer, checked-add overflow through a seeded accumulator/helper, partial
positive reads with an error and a concurrent terminal race. These tests prove
exact underlying calls and returns, one span/duration and no byte sample after
aggregate invalidation. A blocked-Start fixture lets Read, Close and Unwrap win
before Start is released and asserts exported start/end timestamps and duration
exclude the release delay. Plain EOF and wrapped EOF are distinct cases. An
all-stream-signals-disabled fixture proves reader identity is unchanged. After
the runtime proofs pass, atomically promote IDs 6, 7 and 8, append their
availability histories and regenerate schema/wire surfaces.

**Done when:** each observed completed stream emits once; pre-start Unwrap and
the all-signals-disabled path emit nothing. Admitted partial bytes are retained,
raw use after post-start unwrap cannot strand a span, request cancellation state
is not kept alive by the wrapper, nil/typed-nil results retain identity and
nilness, and the same reads/errors/close effects reach the reader.

### [ ] OT-B04. Use the bounded cache data already emitted

1. **Mechanism and source:** counters answer event rate; histograms answer item
   and byte distributions; async gauges answer current in-memory occupancy.
   OTel Go's `metric.Callback` contract requires finite, deadline-aware,
   reentrant, concurrent-safe and globally unique observations; its SDK may
   return a non-nil partial registration together with an error.
2. **Why:** current adapters discard reason, items, bytes and memoized state,
   making saturation, corruption and eviction pressure indistinguishable.
3. **Adaptation:** retain facade/backend layers. Add closed reason and memoized
   attributes, item/value/payload/charged-byte histograms, and optional span
   events. `CacheMemoryStats(tel, backends...)` explicitly registers one
   process-local aggregate callback over a fixed backend set of 1 through
   exported `MaxCacheMemoryStatsBackends = 64`. A larger set is rejected before
   snapshots, singleton reservation or provider work, bounding each O(N)
   collection. Root
   `cachememory` first adds
   `StatsContext(context.Context) (Stats, bool)`: nil/typed-nil/canceled context,
   nil backend or immediate `TryLock` contention returns false; after locking it
   rechecks the context and copies the existing O(1) snapshot. It never waits,
   spins, starts a goroutine or changes the existing blocking `Stats()` API.
   Registration and collection use this bounded snapshot, not `Stats()`.
   Outside the inert disabled path, duplicate non-nil backend pointer identities
   are rejected before any snapshot, provider call or singleton reservation;
   the adapter never deduplicates or double-counts them.
   Registration rejects unavailable snapshots and declared entry/byte-limit
   sums overflowing `int64`. At each collection it checks context before and
   between snapshots, computes and validates the complete aggregate without
   observing anything, then checks context once immediately before emission.
   Any unavailable/invalid snapshot or cancellation during that admission phase
   omits the entire aggregate set; it does not report healthy zero or stale
   data. OTel v1.44 has no atomic multi-instrument Observe operation. Once the
   fixed emission sequence starts it completes without another context check;
   cancellation or an Observer panic may therefore yield partial telemetry but
   can never change cache state or another signal's safe call.
   Otherwise it sums occupancy and limits of active backends only, reports
   active/closed counts, and omits occupancy/capacity observations when none is
   active. `Stats.Closed` is the gate for entries, bytes and both limits. A
   second aggregate registration on the same Telemetry, including value copies,
   is rejected. Only enabled gauges are registered and observed. If Telemetry
   or all six gauges are disabled, return an inert registration without backend
   validation, provider calls or reserving the singleton. The callback is
   finite, deadline-aware, reentrant and concurrent-safe and emits one
   observation per enabled instrument/attribute set. Provider-wide uniqueness
   is an application invariant: the recipe constructs exactly one Telemetry
   per provider/scope and shares it with every adapter. The API cannot detect
   independent Telemetry/native registrations using the same provider; no
   process-global registry is introduced.
   If `RegisterCallback` returns an error with a non-nil registration, deactivate
   the captured callback before immediately attempting Unregister, then drain
   callbacks admitted before deactivation, clear backend references and release
   the local singleton. Join redacted registration/cleanup errors. Nil/typed-nil
   registration is an assembly failure even with nil error. A registration
   panic also deactivates the callback and releases the singleton, so even a
   provider retaining it cannot access backends afterward. An inaccessible
   registration retained by a nonconforming panicking provider cannot be
   unregistered by the adapter; it remains that provider's lifecycle resource.
   Every failure, including a nil/typed-nil result, follows the same available
   portion of deactivate → safe Unregister → drain → clear → release order.
   Callback state strongly owns its fixed backend slice only while active. After
   deactivation and the bounded in-flight drain, success teardown and every
   failure path clear those references before returning, so a retained
   registration handle cannot retain cache backends through adapter state.
   The public surface is
   `type Registration interface { Unregister() error }`,
   `CacheMemoryStats(*Telemetry, ...*cachememory.Backend) (Registration, error)`
   and `MustCacheMemoryStats(*Telemetry, ...*cachememory.Backend) Registration`;
   it never exposes OTel's embedded-marker `metric.Registration`. Constructor
   failures match `ErrAssembly` plus exactly one of
   `ErrInvalidRegistration`, `ErrDuplicateRegistration` or
   `ErrCallbackRegistration`. Cleanup failures match
   `ErrCallbackUnregister`; a partial registration failure may match both
   registration and cleanup sentinels. Native error/panic values are neither
   formatted nor unwrapped. Must panics with the exact returned framework error.
   Public Unregister is idempotent/concurrent and follows exactly: set active
   false under the state lock; call the underlying Unregister through a safe
   path without that lock; wait on the state condition until admitted callbacks
   finish; clear backend/instrument references; token-check and release the
   Telemetry-shared singleton; return the cached redacted cleanup result. Each
   ObserveInt64 call has its own recover boundary, so one provider panic does
   not suppress later enabled gauges.
   External Unregister may race callbacks, but calling that same registration's
   Unregister from inside its callback is unsupported: the pinned SDK holds its
   pipeline lock while invoking the callback and would deadlock on reentrant
   removal. Adapter work is bounded for callback/Observer/provider methods that
   return or panic; it cannot interrupt a nonconforming method that blocks or
   calls `runtime.Goexit` forever.
   Per-instance series stay a native application concern. Fast hits do not
   create spans. Cache keys/namespaces never travel. One public call may emit
   multiple phase events; metric names say `events`, not requests.
4. **Already present:** bounded terminal event vocabularies, observer fan-out,
   event sizes and `cachememory.Backend.Stats`.
5. **Top-level DX:** pass `vvotel.Cache(tel)` and
   `vvotel.CacheMemory(tel)` as observers; optionally call
   `vvotel.MustCacheMemoryStats(tel, primary, secondary)` and defer
   `Unregister`.

**Implementation:** root bounded snapshot method and contention/context tests;
cache mappings/instruments, aggregate async registration using the exact public
surface and error taxonomy above, and tests for facade/
backend separation, eviction reasons, sizes and callback teardown. In the same
change, replace `cache_memory_backend.Stats` with the exact
`StatsContext(context.Context) (Stats, bool)` accessor in the registry source
inventory and all six gauges' sources/inputs, then regenerate schema and wire
manifest. The old blocking accessor remains current application API but is an
explicitly excluded telemetry source until and after that promotion.

**Done when:** dashboard inputs distinguish hit/miss/stale/negative, limit/
corrupt/backend failures and aggregate active occupancy without a cache key/
instance label. Closed backends cannot masquerade as available capacity;
register/poll/unregister error and panic, nil/duplicate/overflowing backends,
disabled gauge subsets, nil/typed-nil and partial-registration cleanup,
idempotent/concurrent teardown, exact `errors.Is` sets, redaction and
callback-after-unregister are covered.
Teardown deactivates new callback entry and waits for already-entered bounded
callbacks before returning; no backend is accessed afterward. Root tests hold
the mutation mutex and prove snapshot returns unavailable without waiting.
A real-SDK race test collects concurrently with Unregister using canceled and
deadline contexts; a captured-callback fake proves failure cleanup deactivation.
An instrumented backend proves the aggregate callback never invokes `Stats()`;
a white-box lifecycle test proves backend references are cleared after the
in-flight drain without relying on finalizer timing. Duplicate-pointer rejection
proves snapshots, provider calls, singleton state and retained references remain
untouched; the all-gauges-disabled path deliberately performs no such validation.
Zero, 64 and 65 backend cases prove the exported work bound without allocating
or registering on rejection. Tests cover concurrent external Unregister but do
not claim callback-reentrant Unregister support.
The same runtime change atomically promotes IDs 12 through 23, appends their
availability histories and regenerates the current schema/wire surfaces; IDs
9 through 11 remain implemented while their richer mappings are updated.

### [ ] OT-B05. Map authentication refusals without credential leakage

1. **Mechanism and source:** security instrumentation counts closed refusal
   reasons and annotates an existing request span instead of creating a second
   authentication span.
2. **Why:** operators need to distinguish missing, ambiguous, rejected and
   broken-guard traffic without seeing credentials or authenticator errors.
3. **Adaptation:** `vvotel.Auth` implements `auth.Observer` and increments the
   unsampled `vv.auth.refusals` counter by the five `ReasonKind` values.
   `vvotel.AuthEvents` is a separate event-only observer for the active span.
   Both ignore `Reason.Detail` and `Reason.Err`. Existing `auth.Sampled` may
   wrap `AuthEvents`; a single boolean-configured observer is deliberately not
   offered because it would let sampling silently make the counter incomplete.
4. **Already present:** a closed five-value reason and panic-isolated multi-
   observer guard seam.
5. **Top-level DX:** pass `auth.Observe(vvotel.Auth(tel))`; when span events are
   useful, also pass
   `auth.Observe(auth.Sampled(n, vvotel.AuthEvents(tel)))`.

**Implementation:** adapter, schema mapping and exhaustive reason/unknown/
panic/provider-failure tests.

**Done when:** each valid reason records once and secret detail/error strings
are absent from span and metric exports.

### [ ] OT-B06. Observe actual health probes, never run extra probes

1. **Mechanism and source:** health telemetry measures the check invocation the
   registry already owns; observable collection must not trigger side effects.
2. **Why:** latency/failure by importance is operationally useful, while an
   OTel callback that pings dependencies would duplicate load and bypass the
   registry's freshness/singleflight policy.
3. **Adaptation:** `vvotel.Health` returns the same `health.Contribution` with a
   wrapped Probe. It preserves name/code/importance/timeout, records check
   count/duration and optionally an INTERNAL span. Metrics use only importance,
   state and bounded error type. Name/code are not exported by default;
   `WithHealthResource(ApprovedName)` may add only a trace `resource.name`.
   Disabled
   contributions remain untouched.
4. **Already present:** per-contribution timeout, panic containment, cached
   shared passes and measured `CheckDetail.Took`.
5. **Top-level DX:** wrap contributions inline before `health.Auto`; omit the
   adapter for native/custom telemetry.

**Implementation:** probe decorator and tests for pass/fail/cancel/timeout/
panic/disabled plus exact contribution preservation.

**Done when:** one registry pass invokes each original Probe once regardless of
metric collection or concurrent health callers.

### [ ] OT-B07. Observe run and drain lifecycle without changing supervision

1. **Mechanism and source:** the existing state observer reports bounded phase
   transitions; a separate lifecycle event reports run/drain completion with
   the operation context. No process-lifetime span is created.
2. **Why:** an early runner return, panic and drain failure must be alertable
   without parsing log text.
3. **Adaptation:** keep the existing `runtime.Observer` source-compatible and
   add an optional `runtime.LifecycleObserver` capability receiving context and
   a closed run/drain event. Add bounded, ordered, panic-isolated
   `runtime.Observers`/`MustObservers` that preserve that capability. Supervisor
   emits running/stopped/failed transitions and each real Drainer completion;
   constructor-only idle and readiness calls are not fabricated as events.
   `vvotel.Runtime` records operation/outcome, placement, durability and ended
   duration. It is metric-only, exports no runner/error name and never starts,
   stops or changes a runner/readiness result.

   The root public contract is exact:

   ```go
   const MaxObservers = 8

   type LifecycleOperation uint8
   const (
       LifecycleOperationRun LifecycleOperation = iota + 1
       LifecycleOperationDrain
   )

   type LifecycleOutcome uint8
   const (
       LifecycleOutcomeOK LifecycleOutcome = iota + 1
       LifecycleOutcomeError
       LifecycleOutcomeCanceled
       LifecycleOutcomeTimeout
   )

   type LifecycleEvent struct { /* immutable, framework-constructed */ }
   func (LifecycleEvent) Operation() LifecycleOperation
   func (LifecycleEvent) Outcome() LifecycleOutcome
   func (LifecycleEvent) Declaration() Declaration
   func (LifecycleEvent) Err() error
   func (LifecycleEvent) Elapsed() time.Duration

   type LifecycleObserver interface {
       ObservedLifecycle(context.Context, LifecycleEvent)
   }
   type LifecycleObserverFunc func(context.Context, LifecycleEvent)

   func Observers(...Observer) (Observer, error)
   func MustObservers(...Observer) Observer
   var ErrTooManyObservers error
   ```

   `Observers` rejects more than eight supplied arguments before nil filtering
   with an error matching only `ErrTooManyObservers`, ignores nil and typed-nil
   entries otherwise, preserves input order, and returns a non-nil inert fan-out
   for zero live children. `MustObservers` panics with that exact returned error.
   The fan-out implements both `Observer` and `LifecycleObserver`; base events go
   to every child and lifecycle events only to children that implement the
   optional capability, with an independent recover boundary around every child.
   `vvotel.Runtime` implements both interfaces.

   A run timer starts immediately before the one `Runner.Run` invocation. Its
   event is delivered after the corresponding terminal `RunnerState` is stored
   and observed, with the exact long-lived runner context and declaration
   snapshot. During `Stop`, a return of nil or an error matching
   `context.Canceled` is the existing expected stop and maps to `run/ok` with a
   nil event error. An unexpected nil return maps to `run/error` with
   `ErrRunnerReturned`; deadline, cancellation and every other returned/contained
   panic error map respectively to timeout, canceled and error. A drain timer
   surrounds each actual `Drainer.Drain` call, uses that exact drain context and
   maps nil/deadline/cancellation/other return to ok/timeout/canceled/error while
   preserving its exact returned error. Drainers still execute concurrently;
   only per-fan-out child order is promised. Non-returning calls and
   `runtime.Goexit` have no fabricated terminal event. A raw Drain panic retains
   the current uncontained panic behavior and is not relabeled as a completed
   drain; observer code cannot cause or contain it.
4. **Already present:** one state-only `runtime.Observer`, typed `RunnerState`,
   supervisor state snapshots and panic-isolated callback invocation; drain
   errors currently bypass it.
5. **Top-level DX:** `runtime.MustObservers(vvotel.Runtime(tel), appObserver)` in
   the existing `runtime.Spec`.

**Implementation:** backward-compatible root lifecycle capability and fan-out,
adapter, context propagation, and run/drain tests including a panicking
neighboring observer. `runtimefx` replaces its private state-only fan-out with
the root fan-out so the optional lifecycle capability survives Fx composition.

**Done when:** running/stopped/failed and the run/drain truth table above are
total; elapsed time surrounds only the one real invocation; the original state
observer still sees exactly the same transitions; normal-stop cancellation is
not counted as a failure; observer failure cannot change state, readiness,
drain calls or `Stop` results. Tests pin 0/8/9 inputs, pre-filter argument
counting, nil/typed-nil handling, exact sentinel/Must panic, callback order and
capability preservation through `runtimefx`.

### [x] OT-B08. Preserve context through `slog` and add console correlation

1. **Mechanism and source:** `slog.Handler.Handle(ctx, record)` is the standard
   context propagation point; the native
   [otelslog bridge](https://pkg.go.dev/go.opentelemetry.io/contrib/bridges/otelslog)
   can export logs when the application selects the OTel Logs SDK.
2. **Why:** `.Error` calls drop the active context before any bridge or handler
   can read the span.
3. **Adaptation:** every framework call site with a context uses
   `ErrorContext`/`WarnContext`/`LogAttrs`, including Supervisor's injected
   application logger. Request-scoped code continues through
   `port.Logger(ctx)`; a long-lived runtime component keeps an existing
   application-owned logger field and passes the operation context to it. No
   binding gains a logger option. `vvotel.TraceHandler(next)` is a stdlib
   `slog.Handler` decorator that adds valid sampled or unsampled `trace_id`,
   `span_id` and `trace_flags`. The triplet is all-or-none: if the current
   record/group or wrapper-supplied `WithAttrs` already owns a reserved key,
   enrichment is skipped rather than overwritten. The handler preserves groups,
   attributes and `Enabled`, never changes globals and performs no redaction.
   Full OTel log export and redaction remain application recipes. Go Logs are
   Beta in the [pinned OTel Go v1.44.0 status](https://github.com/open-telemetry/opentelemetry-go/blob/v1.44.0/README.md).
4. **Already present:** `port.Logger(ctx)` routes a caller-owned logger, but
   eleven verified call sites still need structural enforcement: six HTTP
   transport calls, one gRPC status call, two auth refusal calls, one jobs panic
   call and one Supervisor call. Supervisor is included in those eleven and
   must receive the runner context, not `context.Background()`.
5. **Top-level DX:** `slog.New(vvotel.TraceHandler(jsonHandler))`; low-level
   users pass `otelslog.NewHandler` or any native handler directly.

**Current implementation:** handler, all eleven affected call sites, reciprocal
[[D-062]]/[[D-134]] narrowing, collision/group/WithAttrs tests and an app-owned
redaction + `otelslog` recipe.

**Done proof:** `TestEveryContextBearingFrameworkLogPassesItsContext`
structurally pins all eleven sites and specifically rejects replacing
Supervisor's runner context with a background context.
`TestTraceHandlerCorrelatesWithoutOverwritingCallerFields` behaviourally proves
that a real span correlates a framework error record, no record is modified
without a valid span or on reserved-key collision, and existing attrs/groups
survive byte-for-byte rendering. This is correlation, not a promise to sanitize
existing log values.

### [ ] OT-B09. Measure the complete authenticator chain

1. **Mechanism and source:** an interface decorator measures one logical
   `auth.Authenticator.Authenticate` call and passes the derived context into the
   selected chain.
2. **Why:** refusal events do not cover successful authentication, chain latency
   or infrastructure failure inside authenticators.
3. **Adaptation:** `vvotel.Authenticator(tel, next)` emits one INTERNAL span and
   `vv.authentication.duration` with closed success/refused/error/canceled/
   timeout/panic outcomes. It exports no scheme, token, principal, subject or
   error text and invokes `next` exactly once. Wrap the complete `auth.Chain`,
   not each child, unless child-level native telemetry is deliberately wanted.
4. **Already present:** a small context-bearing interface, conservative chain
   semantics and closed `ErrUnauthenticated` classification.
5. **Top-level DX:** `authenticator = vvotel.Authenticator(tel,
   auth.Chain(jwt, apiKey))`; omit it or use native OTel for a custom chain.

**Implementation:** decorator, registry mapping and result/context/panic/fault
tests with credential/principal privacy canaries.

**Done when:** all outcomes emit once, child spans see the authentication span,
and secrets cannot enter any adapter-created attribute.

### [ ] OT-B10. Observe logical remote calls without duplicating HTTP spans

1. **Mechanism and source:** a transport decorator observes the Frostgrove
   operation while native client instrumentation owns the wire CLIENT span and
   propagation.
2. **Why:** a logical List/Get/Update SLI remains useful across HTTP and future
   transports, but a second HTTP span would double-count the same boundary.
3. **Adaptation:** `vvotel.Remote(tel, next)` emits an INTERNAL span and
   `vv.remote.duration` using only a valid closed `remote.Method` and bounded
   outcome/error type. It never exports ID, query, body, URL, status text or
   `ProtocolError` contents and calls `Do` exactly once. `otelhttp.Transport`
   remains the CLIENT child.
4. **Already present:** one context-bearing `remote.Transport.Do` seam and an
   injectable `http.Client` in `remotehttp`.
5. **Top-level DX:** wrap the logical transport once; low-level users instrument
   only their native client or omit the logical layer.

**Implementation:** decorator, schema and all-method/privacy/context/fault tests.

**Done when:** a recipe produces logical INTERNAL → HTTP CLIENT with one wire
span, and malformed/unknown calls add no arbitrary attributes.

### [ ] OT-B11. Instrument direct CRUD Source and transaction calls exactly

1. **Mechanism and source:** a `crud.Source` decorator observes calls on the
   wrapped handle; optional source capabilities are preserved only by concrete
   outer implementations that perform the effect themselves.
2. **Why:** custom Sources may have no native driver instrumentation, while a
   casual wrapper can silently bypass transactions, replicas or native bulk.
3. **Adaptation:** first add a bounded dependency-neutral
   `crud.ExecutorUnwrapper` navigation seam with
   `UnwrapExecutor() Executor`. Source-semantic helpers walk only
   `SourceUnwrapper`; executor-semantic helpers, including transaction/native
   executor extraction, walk only `ExecutorUnwrapper`. Each is one linear walk,
   never graph search, inspecting at most 64 values including the outer value
   and therefore invoking at most 63 unwrap transitions. The 64th value is still
   checked for the requested capability, but its unwrap method is never called.
   Nil and typed-nil targets, cycles, depth overflow and unknown wrappers fail
   closed. If a wrapper
   implements both seams, the helper being called selects the branch; type-switch
   order never does.
   `vvotel.Source(tel, source)` then instruments direct Exec/Query and returns
   capability-specific concrete wrappers. Beginner and read-source selection
   use the existing bounded `crud.BeginnerOf` and `crud.ReadSourceOf` walks, so
   a supported Source-only wrapper declaring `UnwrapSource` does not lose them.
   The outer `vvotel` implementation calls the discovered Begin exactly once,
   returns an instrumented Tx covering Exec/Query/Commit/Rollback, preserves
   nested Beginner/savepoint discovery, and wraps a discovered replica before
   return. `UnsafeBulkInserterOf` remains stricter: it checks only the immediate
   outer source and never unwraps to grant the capability. Only after that exact
   outer source has supplied `UnsafeBulkInserter` and the outer `unsafe_bulk`
   recorder has started may the implementation follow `ExecutorUnwrapper` to
   locate the transport Tx/Conn used by native COPY. That navigation selects a
   transport target; it never grants permission or bypasses the recorder.
   `Dialect`, `SourceUnwrapper`,
   `ExecutorUnwrapper`, source identity, transaction state, native transaction
   extraction, args/results and errors are preserved. Spans are INTERNAL; SQL,
   args, table/column names and row values never travel. Query measures
   rows-return, not iteration.
4. **Already present:** [[D-062]] defines the direct-call boundary and exact native-
   bulk rule; root helpers discover Begin/read-source/source identity, while
   current adapter extractors only see their concrete outer executor.
5. **Top-level DX:** `source = vvotel.Source(tel, source)` before building
   repositories; complete DB CLIENT/pool telemetry still uses the native driver.
   A graph that gets its source from `crudsqlfx` contributes the same wrapper as
   a `crudsqlfx.Wrapping` named in that module's `Layers` declaration, instead of
   decorating the module's output ([[D-137]]).

**Implementation:** root executor navigation with a fixed depth, adapter
extractor and crudpgx COPY-target updates, capability-specific wrapper matrix
and mutation-resistant tests for primary/replica/Begin/nested savepoint/Tx/
native extraction/transactional native bulk/unwrap/result neutrality. Include
divergent dual-unwrappers, cycles/depth, a source-only intermediary, wrapped Tx,
cross-datasource identity, unknown-wrapper fail-closed behaviour and exact-once
COPY plus exact-once outer recording.

**Done when:** no admitted optional capability appears or disappears, no call
bypasses the outer recorder, each effect happens once, and docs never call it
an all-statements or rows-lifetime tracer. Tests cover both wrapper orders and
a Source-only `UnwrapSource` intermediary for Begin and replica discovery while
proving that the same intermediary cannot regain native bulk. Boundary tests
cover 63, 64 and 65 values, capability on the 64th value and the exact unwrap
call count; no walk performs an ignored 64th unwrap.

### [ ] OT-B12. Measure each periodic pass, not a process-lifetime loop

1. **Mechanism and source:** a context-function decorator measures one bounded
   callback invocation; `runtime.Periodic` continues to own ticks, timeout and
   panic containment.
2. **Why:** supervisor state says whether a loop died, but a periodic task may
   repeatedly fail while the runner correctly stays alive.
3. **Adaptation:** `vvotel.Periodic(tel, pass)` returns the same
   `func(context.Context) error`, emitting one INTERNAL span and duration metric
   for success/error/canceled/timeout/panic. It re-panics unchanged so the
   existing periodic containment remains authoritative. No timer/goroutine/name
   is created; `WithPeriodicResource(ApprovedName)` may add only a trace
   `resource.name`.
4. **Already present:** `PeriodicSpec.Pass` is a context-bearing bounded callback
   and failures intentionally do not stop the schedule.
5. **Top-level DX:** `runtime.Every("cleanup", interval,
   vvotel.Periodic(tel, cleanup))`; pass the original callback for zero wrapper.

**Implementation:** callback decorator and exact call/context/outcome/panic tests.

**Done when:** repeated failures produce separate completed observations without
changing cadence, timeout, logging or runner lifetime.

## C. Durable jobs

### [ ] OT-C01. Make the existing trace carrier composable

1. **Mechanism and source:** decorator-friendly immutable values expose safe
   accessors and copy-with methods while retaining validation and hidden
   identity fields.
2. **Why:** `ContextCapture` and `RestoredIdentity` are intentionally opaque, so
   `vvotel` cannot add tracing without dropping tenant/actor/token identity.
3. **Adaptation:** add `ContextCapture.Trace() UntrustedTraceCarrier`,
   `ContextCapture.WithTrace(UntrustedTraceCarrier) (ContextCapture, error)`,
   `IdentityRestoreRequest.Trace() UntrustedTraceCarrier`, and
   `RestoredIdentity.WithContext(context.Context) (RestoredIdentity, error)`.
   Each returns a validated copy and none exposes protected identity.
   `WithContext` can prove only non-nil input, unchanged private lineage and the
   same Done channel/deadline; a general Go context cannot reveal or enumerate
   all values or guarantee a future cancellation cause. The built-in
   `vvotel.JobIdentity` owns the stronger promise: it derives only with
   `trace.ContextWithRemoteSpanContext(identity.Context(), spanContext)`, so
   existing application values and delayed cancellation cause are retained.
   Add `SystemContextProvider() TrustedContextProvider`, which returns the same
   `framework.system`/epoch-1 empty-identity capture currently synthesized when
   `QueueSpec.Context` is absent, and
   `SystemIdentityRestorer() TrustedIdentityRestorer`, which returns
   `NewRestoredIdentity(ctx, ProducerPartition{}, ProducerActor{})` for an exact
   system-scope request and rejects a tenant-scope request. Both preserve the
   invocation context and expose no configurable identity shortcut. This lets
   tracing global jobs reuse the existing default without manufacturing
   provenance. Do
   not tighten the existing durable-record grammar: previously accepted trace
   flags and level-two tracestate keys must remain restorable. Instead define a
   stricter admission check for newly OTel-injected carriers and a separate
   legacy-compatible restore path. Durable binding still covers the trace
   carrier and identity seal.
4. **Already present:** bounded W3C fields/correlations, TracePolicy validation,
   durable binding digest and same-context-lifetime/lineage checks.
5. **Top-level DX:** the same decorators work for system and tenancy jobs;
   callers do not rebuild opaque captures.

**Implementation:** root jobs APIs, explicit new-admission versus legacy-restore
validation, tests for preservation/tampering/lineage and updates to tenancy jobs
tests. System-building-block tests prove equality with the queue's existing
default capture, exact-one invocation-context forwarding and tenant-scope
rejection. The unpublished test module runs a differential corpus/property suite
against pinned `propagation.TraceContext` and `trace.ParseTraceState`, including
v00 flags, digit-leading/simple and tenant@system keys, duplicate/multiple `@`,
member and length bounds. Invalid or legacy-incompatible tracestate never
invalidates an otherwise valid traceparent at the OTel extraction boundary.

**Done when:** adding/extracting trace data changes no tenant/actor/token field,
an altered carrier fails durable validation, every newly OTel-admitted carrier
yields the declared upstream remote SpanContext, and every legacy fixture still
restores. `WithContext` rejects nil, lineage replacement and lifetime changes;
JobIdentity sentinel-value and delayed-cancellation tests prove the stronger
built-in derivation contract. Legacy tracing that upstream cannot parse is
dropped or reduced to its valid traceparent without failing identity restoration
or job execution.

### [ ] OT-C02. Inject and extract W3C Trace Context at the durable boundary

1. **Mechanism and source:** OTel `propagation.TraceContext` injects
   `traceparent`/`tracestate`; baggage is a separate security-sensitive signal.
2. **Why:** a job executed after delay, retry or process restart must retain
   causal correlation without keeping a span open.
3. **Adaptation:** `vvotel.JobContext` decorates a
   `jobs.TrustedContextProvider`, calls it once, then injects only W3C Trace
   Context into its validated copy. `vvotel.JobIdentity` calls the trusted
   restorer once, extracts a remote span context, and returns a copy whose
   context retains the original lifetime and identity lineage. Malformed or
   unsampled valid context follows OTel rules; invalid carrier never becomes
   trusted identity. Correlation fields are preserved but never auto-exported.
   The built-in adapters always use `propagation.TraceContext{}`; low-level
   callers can use the native propagation API and root copy-with methods. A
   legacy carrier rejected by upstream drops only tracing (or invalid
   tracestate), increments a closed propagation outcome metric and returns the
   original successfully restored identity context; observability cannot reject
   a previously valid job.

   The public decorators are exactly
   `JobContext(*Telemetry, jobs.TrustedContextProvider) jobs.TrustedContextProvider`
   and
   `JobIdentity(*Telemetry, jobs.TrustedIdentityRestorer) jobs.TrustedIdentityRestorer`.
   They return nil for a nil/typed-nil dependency, invoke a present dependency
   exactly once, and return its exact value/error or panic unchanged. Decoration
   and the one propagation metric happen only after a nil-error base result; a
   telemetry failure never replaces that result.

   Injection preserves every base capture field and correlation. With no valid
   current SpanContext it leaves the base trace carrier byte-for-byte unchanged
   and records `absent`. With a valid SpanContext it constructs a fresh
   traceparent/tracestate pair rather than injecting into the old pair, so a new
   empty tracestate clears stale provider tracestate. Admission is deterministic:

   | Candidate retained with the base correlations | Returned capture | Outcome |
   |---|---|---|
   | new traceparent + new tracestate fits | copy with both | `injected` |
   | full pair does not fit/validate, traceparent alone fits | copy with new traceparent and empty tracestate | `tracestate_dropped` |
   | traceparent alone does not fit/validate | exact base capture | `traceparent_dropped` |

   `MaxTraceCarrierBytes` is checked by `ContextCapture.WithTrace`; correlations
   are never truncated or removed to make tracing fit. No path retains an old
   tracestate beside a new traceparent.

   Extraction calls the restorer first. A zero traceparent records `absent` and
   returns its exact identity. An invalid traceparent records
   `traceparent_dropped` and returns the exact identity context. A valid
   traceparent with absent/valid tracestate records `extracted`; a non-empty
   tracestate rejected by pinned `trace.ParseTraceState` is discarded while the
   valid remote parent is attached and records `tracestate_dropped`. Attachment
   is exactly
   `trace.ContextWithRemoteSpanContext(identity.Context(), spanContext)` followed
   by `RestoredIdentity.WithContext`; if that copy fails its lineage/lifetime
   checks, the exact base identity is returned and the outcome is
   `traceparent_dropped`. Each successful base call records exactly one of the
   five closed outcomes. Base error/panic paths record none. Metric panics or
   disabled telemetry do not change the table.
4. **Already present:** durable traceparent/tracestate storage and producer/
   worker trust policies; composition access is the missing piece.
5. **Top-level DX:** wrap the existing tenancy or new root system pair once in
   queue and workers configuration; use the root carrier API for a custom
   propagation policy.

**Implementation:** carrier adapter, capture/restore decorators and real-SDK
restart/malformed/unsampled tests.

**Done when:** producer and worker use different provider instances/process
fixtures yet the worker sees the original remote SpanContext, with no baggage.
Tests exhaust absent/valid current span × zero/existing provider trace × full/
traceparent-only/no-capacity admission, stale-tracestate clearing, malformed
traceparent, malformed-only tracestate, unsampled context, exact correlation and
base value/error preservation, and one-outcome precedence.

### [ ] OT-C03. Trace and measure enqueue without claiming transaction commit

1. **Mechanism and source:** messaging producer spans cover the synchronous
   enqueue call; transactional staging is an INTERNAL intent operation until
   the caller's transaction commits.
2. **Why:** propagation alone connects traces but does not show encoding,
   admission or sender latency and failures.
3. **Adaptation:** [[D-134]] explicitly permits typed `vvotel.Enqueue`,
   `EnqueueOnce`, `EnqueueIn` and
   `EnqueueOnceIn` call the corresponding jobs function exactly once.
   Non-transactional calls use PRODUCER spans; staged calls use INTERNAL spans
   and outcome `staged`, never `published` or `committed`. Metrics record
   duration/outcome/error type without definition, invocation, intent or
   partition labels. The producer span starts before context capture so its
   W3C context is persisted.
4. **Already present:** four generic enqueue functions, typed conservative
   errors and transaction-aware staged results.
5. **Top-level DX:** import `vvotel` for observed enqueue; use native
   `jobs.Enqueue*` for the exact low-level path.

**Implementation:** four wrappers, schema and effect/order/transaction wording
tests.

**Done when:** each wrapper preserves IDs/results/errors/options and a staged
enqueue trace cannot be interpreted as a commit assertion.

### [ ] OT-C04. Give each handler invocation its own linked CONSUMER span

1. **Mechanism and source:** OTel messaging conventions model processing with a
   CONSUMER span; span links preserve causality when retries and long delays
   should not share one sampling/root lifetime.
2. **Why:** each actual handler invocation has its own latency, queue delay and
   return outcome; one span across the durable invocation would survive sleeps/
   restarts and lie about active work.
3. **Adaptation:** `vvotel.Job(tel, definition, handler,
   workerOptions ...jobs.WorkerOption)` returns a normal Consumer using the
   default new-root + producer-link policy without weakening sealed worker
   options. Low-level `vvotel.JobAdapter(tel, adapter,
   telemetryOptions...)` wraps an existing `jobs.AdapterHandler`; callers pass
   it to `jobs.OnAdapter` with worker options separately. It derives only an
   active-span context while preserving the original Done channel, deadline,
   cancellation cause and values; it forwards the exact payload, DeliveryMeta
   and same AttemptController, including Pulse and Guard effects. It records
   handler duration, eligible queue delay and attempt
   positive ordinal, bounded by `jobs.MaxAttemptOrdinal` (4129);
   definition/binding are absent unless explicitly approved and never
   metric labels. Parent-child is an explicit JobAdapter option. Handler return
   describes only handler outcome; timeout arbitration, disposition and Apply
   remain outside. The span ends when the handler returns, even if the worker
   already timed out; a handler that never returns truthfully leaves its span
   open. Panic is recorded and re-panicked for existing containment.
4. **Already present:** `OnAdapter`, immutable DeliveryMeta timestamps,
   attempt controller and worker panic/error classification.
5. **Top-level DX:** `consumer := vvotel.Job(tel, definition, handler,
   jobs.Concurrency(4))`; low-level users call
   `jobs.OnAdapter(definition, vvotel.JobAdapter(tel, adapter,
   vvotel.ParentChild()), workerOptions...)`.

**Implementation:** typed standard factory, exact adapter wrapper, link policy,
metrics and success/error/panic/cancel/timeout-controller tests. Retry/defer/
permanent disposition belongs to worker classification and Apply telemetry, not
this wrapper.

**Done when:** two returned handler invocations produce two ended spans linked to
the same producer; a timed-out non-cooperative handler's span ends only on its
   later return; changing context lifetime/cause/values, meta, controller,
   result or panic forwarding fails a test; no invocation/tenant/payload ID
   appears in either signal. No assertion
calls the span a complete attempt or final delivery.

### [ ] OT-C05. Convert worker lifecycle events into bounded metrics

1. **Mechanism and source:** worker control-plane metrics are separate from
   messaging handler metrics; event context supplies exemplars when one exists.
2. **Why:** claim starvation, renew failure, stale leases, apply ambiguity,
   admission saturation and forced drain are not visible from handler spans.
   The current `observeApply` also always copies the command-level reason: for a
   `finish_attempt` command with a nonzero disposition this drops the
   disposition's handler-failure, panic or other reason, contradicting the
   public WorkerEvent "disposition and why" contract before OTel sees it.
3. **Adaptation:** pass the actual operation context to every WorkerObserver
   emission. In root `observeApply`, when `DeliveryCommand.Disposition()` is
   nonzero, set `WorkerEvent.Reason` from `Disposition.Reason()`; otherwise use
   `DeliveryCommand.Reason()`. Then `vvotel.Workers` records operation
   count/duration, processed items and bytes, admission outcomes/signals, and
   delivery mutation/control result counts. It maps only closed enums.
   Items are bounded by 1000, bytes by `jobs.MaxClaimBytes` (67108864), nested
   delivery-result items to 1..`jobs.MaxClaimItems` (256), and recovery releases
   by `jobs.MaxReclaimBatch` (1000). Every projection validates the outer event
   operation/outcome before recording.
   Definition/binding, payloads and IDs are excluded from metric labels. It
   emits no fake span for polling operations and never derives backlog from
   local counters. Correcting the root event does not add workflow state,
   history or orchestration.
4. **Already present:** a bounded WorkerEvent vocabulary with elapsed/count/
   byte/admission/disposition data and an eight-child isolated fan-out.
5. **Top-level DX:** add `vvotel.Workers(tel)` to the existing fan-out; all
   custom observers continue to run.

**Implementation:** root context fix; root `observeApply` disposition-reason
fix; worker adapter; and exhaustive valid command/disposition/reason plus
operation/outcome/result mapping tests.

**Base proof:** `TestWorkerObservationPreservesEachOperationContextAndEffectiveApplyReason`
covers exact contexts plus representative nonzero-disposition override and
zero-disposition fallback. The exhaustive valid matrix remains O4.

### [ ] OT-C06. Observe scheduler cycles without building a workflow engine

1. **Mechanism and source:** a jobs-owned terminal observer reports one bounded
   `Scheduler.RunDue` cycle and its aggregate placement result.
2. **Why:** a Scheduler may keep running while cycles place nothing, conflict or
   fail; handler and worker signals cannot answer whether schedules are firing.
3. **Adaptation:** add a context-bearing, panic-isolated
   `jobs.ScheduleObserver`, bounded event and fan-out to `SchedulerSpec`.
   `vvotel.Scheduler` records cycle duration/outcome and Due/Placed/Existing/
   Conflicts distributions, each bounded by `jobs.MaxDefinitions` (4096). It
   exports no schedule/definition/intent/invocation
   name or due timestamp, starts no ticker and never changes `RunDue` results.
   It is a scheduler-cycle metric adapter, not durable workflow history,
   orchestration, replay or a Temporal substitute.

   The root public contract is exact:

   ```go
   const MaxScheduleObservers = 8

   type ScheduleEvent struct { /* immutable, scheduler-constructed */ }
   func (ScheduleEvent) Result() ScheduleRunResult
   func (ScheduleEvent) Err() error
   func (ScheduleEvent) Elapsed() time.Duration

   type ScheduleObserver interface {
       Observe(context.Context, ScheduleEvent)
   }
   type ScheduleObserverFunc func(context.Context, ScheduleEvent)

   func ScheduleObservers(...ScheduleObserver) (ScheduleObserver, error)
   func MustScheduleObservers(...ScheduleObserver) ScheduleObserver
   ```

   `SchedulerSpec` gains `Observer ScheduleObserver`. The fan-out rejects more
   than eight supplied arguments before nil filtering with an error matching
   `jobs.ErrTooLarge`, ignores nil/typed-nil entries otherwise, preserves input
   order, isolates every child panic, and returns a non-nil inert fan-out for
   zero live children. `MustScheduleObservers` panics with the exact returned
   error.

   Nil receiver/context and a failed cycle CAS are pre-admission conflicts and
   emit nothing. A successful CAS admits one cycle and starts elapsed timing
   before the context and clock checks. From that point, pre-canceled context,
   clock failure, a partial `runDue` failure and success each emit exactly one
   immutable event after the cycle result/error is final. It receives the exact
   `RunDue` context, exact returned `ScheduleRunResult` including any partial
   counts, exact returned error, and non-negative elapsed time. The observer is
   invoked before the CAS guard is released, so a reentrant `RunDue` remains the
   same conflict it is without observation; observer faults are contained.
   `Scheduler.Run` obtains telemetry only through each real `RunDue` call and
   adds no loop-level or timer event.
4. **Already present:** `ScheduleRunResult` is closed aggregate data and
   `RunDue` is the single cycle boundary used by both manual calls and `Run`.
5. **Top-level DX:** set `Observer: vvotel.Scheduler(tel)` in `SchedulerSpec`;
   compose it with `jobs.MustScheduleObservers` when the application also
   observes cycles.

**Implementation:** root schedule event/observer/fan-out, adapter, schema and
cycle success/error/cancel/panic-isolation/result-neutrality tests.

**Done when:** manual and loop-driven admitted cycles emit exactly one event
with the same aggregate result/error returned by `RunDue`; pre-admission
conflicts emit none; canceled-before-clock, clock failure, partial placement
failure and success follow the truth table above. Tests pin 0/8/9 inputs,
pre-filter argument counting, nil/typed-nil handling, exact `ErrTooLarge`/Must
panic, child order/reentrancy/panic isolation, and prove no schedule identity
becomes a metric label.

### [ ] OT-C07. Prove the complete producer → restart → retry path

1. **Mechanism and source:** distributed-trace conformance is an end-to-end
   property across injection, durable serialization, restoration, links and
   metrics exemplars.
2. **Why:** unit tests of each adapter can all pass while one storage or restore
   boundary strips tracestate or changes parentage.
3. **Adaptation:** use the real job memory backend for deterministic restart and
   the existing durable record representation. Enqueue under a sampled span,
   recreate queue/workers/providers, force one retry, then succeed. Assert one
   producer span, two linked returned-handler spans, distinct handler span IDs,
   preserved tracestate, queue-delay/handler/worker metrics and no forbidden
   fields. This causal fixture calls the OT-C03 enqueue wrapper directly;
   scheduler-cycle telemetry is proven independently by OT-C06 because the
   scheduler's internal `jobs.EnqueueOnce` call is not an OT-C03 call-site
   wrapper. Add malformed and jobs-valid/OTel-invalid regression corpus controls.
4. **Already present:** memory backend end-to-end tests and real SDK test module
   dependencies.
5. **Top-level DX:** this becomes a copyable application test fixture, not a
   runtime framework service.

**Implementation:** unpublished integration fixture plus a malformed-carrier
control.

**Done when:** breaking any one capture/serialization/restore/link step fails
the scenario.

## D. Application SDK, native instrumentation and operations

### [ ] OT-D01. Ship native HTTP, gRPC, database and Go-runtime recipes

1. **Mechanism and source:** native middleware/StatsHandlers own the single wire
   SERVER/CLIENT boundary; driver hooks own DB calls. The application supplies
   providers, propagation, trust policy and bounded metric Views. The concrete
   import/version/options roster below is part of this card.
2. **Why:** Frostgrove INTERNAL spans are useful only when composed with wire,
   database and runtime evidence; duplicating those mature libraries would
   produce double spans and weaker semantics.
3. **Adaptation:** each recipe passes `WithTracerProvider`, `WithMeterProvider`
   and an explicit TraceContext propagator wherever the integration accepts
   them. Trusted/client paths use `WithPropagators(propagation.TraceContext{})`;
   public servers use an application propagator delegate whose Extract calls
   TraceContext then clears the extracted SpanContext's TraceState without
   modifying request headers. Database and runtime options are listed below.
   Default recipes call no `otel.Set*`; a provider or propagator omitted from a
   native constructor is a test failure. Trusted ingress keeps the remote parent.
   Public ingress creates one new SERVER root with a link containing only the
   remote trace/span IDs and flags; untrusted tracestate is removed and baggage
   is never extracted. Native HTTP/gRPC public-endpoint options and the explicit
   Gin/Fiber delegate below implement that policy without another span.
   HTTP CLIENT spans cover response-body EOF/Close, while the native duration
   metric's actual endpoint is documented separately. SQL text/parameters and
   SQLCommenter are disabled; DB span names come from fixed operation names,
   never SQL text. Native error/URL fields require the OT-D06 export policy.
4. **Already present:** net/http, Gin, Fiber v3 and gRPC bindings, injectable
   HTTP clients/pools and a stdout SDK bootstrap example; none is natively
   instrumented by `vvotel`.
5. **Top-level DX:** copy a stack-specific recipe, keep every native option, and
   remove any layer independently.

**Implementation:** compile-tested recipes for net/http, Gin, Fiber v3, gRPC,
HTTP clients, pgx/`database/sql` and Go runtime; composition diagrams in both
OTel module docs.

**Pinned integration roster:** these module versions were downloaded and their
source/options checked against the repository's OTel `v1.44.0`, Gin `v1.12.0`,
Fiber `v3.4.0`, gRPC `v1.83.1` and pgx `v5.10.0` baseline. They are recipe pins,
not yet added dependencies or proof that the future recipe compiles. Every row
requires a separate `GOWORK=off` compile fixture using these application versions;
SDK/bridge dependencies belong only to unpublished examples/tests.

| Boundary | Exact import and pin | Explicit assembly and limits |
|---|---|---|
| [net/http server + client](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.69.0) | `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0` | `NewHandler`/`NewTransport` with both providers and TraceContext; server `WithPublicEndpointFn`, a fixed pre-dispatch `WithSpanNameFormatter`, and the application-owned post-dispatch `Request.Pattern` policy below |
| [Gin](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin@v0.69.0) | `go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin v0.69.0` | `Middleware` with both providers, TraceContext and `WithSpanNameFormatter` over the approved route/method table; no native public-endpoint option exists at this pin |
| [Fiber v3](https://pkg.go.dev/github.com/gofiber/contrib/v3/otel@v1.2.0) | `github.com/gofiber/contrib/v3/otel v1.2.0` | `Middleware` with both providers, TraceContext, `WithPort`, `WithClientIP(false)` and bounded `WithSpanNameFormatter`; no native public-endpoint option exists at this pin |
| [gRPC server + client](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@v0.69.0) | `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.69.0` | `NewServerHandler`/`NewClientHandler` with both providers and TraceContext; server `WithPublicEndpoint`; StatsHandler owns unary and streaming lifetimes |
| [pgx](https://pkg.go.dev/github.com/exaring/otelpgx@v0.11.1) | `github.com/exaring/otelpgx v0.11.1` | `NewTracer` with both providers, `WithDisableSQLStatementInAttributes`, `WithDisableConnectionDetailsInAttributes`, `WithTrimSQLInSpanName` and `WithSpanNameFunc` returning the constant `statement`; the scoped tracer delegate below enforces all span names, including COPY; query parameters stay disabled |
| [database/sql](https://pkg.go.dev/github.com/XSAM/otelsql@v0.43.0) | `github.com/XSAM/otelsql v0.43.0` | `Open`/`OpenDB` with both providers, `WithTextMapPropagator(propagation.TraceContext{})`, `WithSQLCommenter(false)`, `WithSpanOptions(SpanOptions{DisableQuery: true, RecordError: func(err error) bool { return !errors.Is(err, sql.ErrNoRows) }})` and fixed `WithSpanNameFormatter`; this callback changes span error recording only; `RegisterDBStatsMetrics` receives the scoped defensive MeterProvider below and its guarded registration is retained |
| [Go runtime](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/runtime@v0.69.0) | `go.opentelemetry.io/contrib/instrumentation/runtime v0.69.0` | `Start(WithMeterProvider(metrics))` exactly once for that provider; there is no unregister handle, so its callback lives until provider shutdown |

For net/http, `otelhttp.NewHandler` is outside an application-owned route-policy
handler, which is outside a private `http.ServeMux`. Every application route is
registered through a distinct comparable wrapper carrying its full ServeMux
pattern and a private token; the route table is sealed before the instrumented
handler can serve. Before dispatch the formatter emits one fixed fallback span
name. The route-policy calls `ServeMux.Handler` and admits a candidate only when
the returned handler is the exact registered wrapper for the returned full
pattern. It then dispatches through the same mux while the wrapper records its
token in request-private state before calling application code. After dispatch,
the policy restores the captured full `Request.Pattern` only when candidate and
executed tokens are identical; otherwise it clears the field before returning
to outer `otelhttp`. Restoring the captured value makes nested ServeMux or
handler mutation unable to change the admitted route. This rejects
ServeMux-generated slash/path-cleaning/CONNECT redirects: `Handler` reports a
target pattern for those, but the registered wrapper never executes. It also
rejects unmatched, malformed and concurrently substituted routes without
comparing arbitrary handlers or reading `URL.Path`.

The admission domain is the allow-list of full ServeMux patterns, including
optional method and host qualifiers. The native metric domain is a separate,
deduplicated projection: pinned otelhttp v0.69.0 derives `http.route` by keeping
the substring from the first `/`. Thus `GET /items/{id}` and
`POST /items/{id}` both project to `/items/{id}` and remain distinguishable by
the admitted method attribute; host-qualified variants intentionally aggregate
when `server.address` is dropped. Views and the OT-D05 native budget enumerate
these projected values, while dispatch, tamper checks and management-route
classification use full patterns. The bridge may rename the span only from the
admitted full pattern/method table; it never reads the raw path. This route
policy is required independently of the span formatter because server metrics
read `Request.Pattern` directly before applying that projection.

The `GOWORK=off` fixture covers ordinary, method-qualified and host-qualified
patterns, projection collisions, HEAD-via-GET, unmatched/malformed paths,
slash/path-cleaning/CONNECT redirects, `RequestURI == "*"`, many raw IDs and a
handler delegating to a nested ServeMux that replaces `Request.Pattern`. It
proves captured-pattern restoration for an executed wrapper, and fallback span
naming plus absent `http.route` whenever no registered wrapper ran. Registration
after sealing fails before serving and concurrent requests cannot exchange
admission tokens. Exact full-pattern tests also prove that a host- or
method-qualified `/live` or `/ready` is not accidentally treated as the
path-only management route.

Pinned `otelsql.RegisterDBStatsMetrics` discards a non-nil registration when
its `Meter.RegisterCallback` returns `(registration, error)`, while OTel SDK
v1.44 permits that partial result. The application recipe therefore passes this
helper a scoped MeterProvider/Meter delegate. The delegate wraps the callback
in deactivation/in-flight state and intercepts RegisterCallback. On partial
error, nil/typed-nil registration or panic it performs the available portion of
deactivate → safe Unregister → drain → clear, then returns a redacted error that
joins any cleanup failure. On success it returns a guarded registration whose
idempotent/concurrent Unregister uses the same order. A provider-retained
callback becomes inert before cleanup returns and cannot touch `*sql.DB`.
The delegate wraps only this application assembly edge; it is not added to
`vvotel`, does not mutate globals and forwards every other native Meter method.
Hostile fixtures inject `(registration, error)`, nil/typed-nil results, panic,
concurrent callback/unregister and callback retention; the ordinary fixture
also proves the pinned real SDK path.

Default fixtures leave `OTEL_SEMCONV_STABILITY_OPT_IN` unset. Each opt-in mode
requires its own pinned native-manifest fixture; an environment-driven semconv
change cannot silently reuse another mode's schema or budget.

Gin/Fiber public-edge recipes supply an application-owned TracerProvider
delegate scoped to that native instrumentation. At its single `Tracer.Start`,
it adds `trace.WithNewRoot` and a sanitized remote `trace.Link`; it forwards the
remaining options and creates no additional span. Trusted mode forwards the
original parent. Fiber's pin initially uses a raw path before its final
formatter: this delegate replaces that initial name with a fixed name before
the SDK, and the final formatter admits only registered templates. This small
application adapter is explicit because the pinned bridges lack the required
native option; it never enters `vvotel` or a transport binding.

pgx's `WithSpanNameFunc` affects Query/Prepare/Batch names only when
`WithTrimSQLInSpanName` is also set; neither option changes its
`copy_from <table>` name. The application therefore passes a TracerProvider
delegate to `otelpgx.NewTracer` which intercepts only the
`github.com/exaring/otelpgx` instrumentation scope. Its tracer normalizes every
Start name before calling the borrowed tracer once: `query statement` →
`db.query`, `prepare statement` → `db.prepare`, `batch start` → `db.batch`,
`copy_from ` followed by any suffix → `db.copy`, `connect` → `db.connect`, and
`pool.acquire` → `db.acquire`. Any other input becomes `db.operation`; no SQL
token or table suffix is copied into the result. Already-normalized names are
accepted unchanged. The returned span and span in the returned context share
a delegate applying that same mapping to SetName; every other method and
context property is forwarded. Other instrumentation scopes are untouched.
This handles the native COPY exception without adding a span or changing any
pgx hook's input/result. OT-D06 independently projects exported span Name through
the same scope-specific allowlist and fixed fallback.

Native metric Views are scope/instrument-specific and filter keys **and
values** before aggregation. HTTP permits only the deduplicated native
`http.route` projection of the sealed full-pattern table, the nine standard
`http.request.method` values plus `_OTHER`, and status codes `100..599`; a
rejected/missing value contributes only the absent-attribute variant. The
manifest records both the full admission table and its pinned projection so a
collision or upstream projection change is reviewed explicitly. Host-derived
`server.address`/port, raw URL, protocol/version,
forwarded/client address, user agent, native error text/type and custom extra
attributes are dropped from these metric streams. Client endpoints and DB pool
names, when retained by their own Views, come only from fixed application
configuration. gRPC admits the declared service/method table and codes `0..16`;
an application StatsHandler delegate passes a copied `RPCTagInfo` with a fixed
`/_OTHER/_OTHER` name for an unknown FullMethodName to instrumentation only,
leaving actual dispatch untouched. The versioned OT-D05 manifest enumerates
each instrument's exact attribute subset, absent variants and calculated bound.

pgx `RecordStats` at this pin returns no teardown handle, so the recipe instead
owns one native metric callback over `pgxpool.Stat`, with bounded pool-name
attributes and an explicit registration handle. It measures acquired/max/idle
connections and acquire waits/time without issuing SQL. Expected
`pgx.ErrNoRows`/`sql.ErrNoRows` is tested at the layer where it is returned. The
ordinary `database/sql` empty-row case is asserted through `QueryRow().Scan`:
it is synthesized after the driver span, while a native-hook `sql.ErrNoRows` is
excluded by `RecordError`. Row iteration is checked separately from Close: the
pinned `otelsql` span sees a non-EOF `Rows.Next` error, but its duration callback
receives the Close result and therefore cannot be treated as an iteration-
outcome metric. True driver errors remain failures.

Pinned `otelsql v0.43.0` constructs its deferred duration recorder before
`createSpan` and closes over the incoming context. Its duration exemplar
therefore identifies the immediate caller span, not the DB CLIENT span. The O2
full-stack canary asserts that exact upstream behavior instead of claiming false
DB-span correlation. O3 re-checks newer pins or a narrowly owned application
bridge; concurrent parent-to-child guessing is not an acceptable projection.

**Done when:** one sample request shows SERVER → command INTERNAL → DB CLIENT
parentage without duplicate boundary spans. Public-edge fixtures get a new
trace ID plus a link while trusted ingress remains a child. Net/http, Gin,
Fiber and gRPC fixtures send many IDs through one route plus unmatched/malformed
requests and prove bounded span names and route-metric series; raw path, query,
header or ID never becomes a metric label or span name. Any native URL
attribute is governed and tested by the application privacy policy documented
under OT-D06, not misrepresented as a `vvotel` guarantee.
Unary and streaming gRPC lifetimes, HTTP body EOF/read-error/early-close and
database pool/no-row policies are asserted. Host/address/port, arbitrary HTTP
methods/statuses, unknown gRPC FullMethodName, malformed routes and injected
extra attributes cannot increase a metric beyond the native budget manifest.
The scanner counts each instrument/resource/scope independently. Global-provider
sentinel fixtures prove all signals and propagation use the explicitly supplied
objects; public-edge canaries also prove tracestate and baggage are absent.
Recording-parent fixtures invoke Query, Prepare, Batch and COPY with secret
SQL literals, prepared-statement names and table identifiers. They assert the
exact fixed span names at SDK Start/SetName and exported OTLP, including the
unknown-name fallback; none of those secrets survives in names or fields.
Each scenario asserts that its native hook emitted a span, and an unprotected
control demonstrates the pinned native SQL/COPY-name or attribute exposure.

### [ ] OT-D02. Ship an app-owned production OTLP SDK recipe

1. **Mechanism and source:** OTel SDK Resource, batch span processor, periodic
   metric reader, Views, exemplar filter, `ForceFlush`/`Shutdown` and OTLP
   exporters are application concerns.
2. **Why:** users need a production starting point with correct lifecycle, but
   putting it in `vvotel` would turn a microkernel adapter into a bootstrap
   framework.
3. **Adaptation:** an unpublished recipe builds Resource with
   `service.name/version/namespace`, OTLP trace/metric exporters, bounded batch/
   interval settings, trace-based exemplars and scope-targeted histogram Views.
   Sampling is an explicit recipe parameter; the ordinary production default
   is ParentBased ratio sampling, while OT-D03's tail variant supplies the
   always-on head sampler required by its stronger retention claim. Before an
   exporter is attached to its processor/reader, constructor rollback owns and
   shuts it down directly.
   Attachment transfers ownership to that processor/reader; rollback before a
   provider exists shuts down this owner. Once it is registered with a provider,
   rollback and normal shutdown invoke only the provider, never shut the
   attached exporter down a second time. Partial construction has an explicit
   owner at every return/error boundary.
   The recipe returns `ForceFlush(ctx)` and `Shutdown(ctx)`. Shutdown executes
   once, caches its joined result and returns that same result to concurrent
   or repeated callers. It attempts both providers even if one fails. Flush
   and shutdown are coordinated so a new flush cannot enter after shutdown
   starts.

   The returned lifecycle handle has an explicit `open → closing → closed`
   state machine and recipe-local `ErrTelemetryClosed`. A ForceFlush linearized
   while open is admitted, increments an in-flight count, calls both providers
   under separately created trace/metric budgets, joins their results, then
   releases its admission. The first Shutdown atomically changes open to closing
   and admits no more flushes; a concurrent or later ForceFlush calls neither
   provider and returns the exact `ErrTelemetryClosed`. The shutdown owner waits
   for every already-admitted flush, then attempts both provider Shutdown calls
   with independent configured deadlines derived from
   `context.WithoutCancel(ownerContext)`, caches their joined result, changes to
   closed and wakes waiters. It performs that work in the owning call, with no
   hidden cleanup goroutine.

   A Shutdown arriving in closing state waits for completion or its own context,
   whichever occurs first. If its own context wins it returns that context error
   only to that waiter and neither cancels cleanup nor poisons the cached result;
   it may call again. A waiter that sees completion and every Shutdown arriving
   in closed state receives the same cached error value produced by the owner,
   regardless of its now-canceled context. The owner's cancellation/deadline is
   deliberately removed before the separately bounded provider cleanup calls;
   only its values survive. Already-admitted ForceFlush retains its own context
   contract. These rules make the trace/metric SDK differences after native
   shutdown unobservable at the recipe surface. Environment variables use
   upstream OTel names; every default
   assembly passes native objects explicitly without global setters.
4. **Already present:** a stdout example with explicit provider shutdown.
5. **Top-level DX:** copy `newTelemetry(ctx, config)` into the application and
   pass the returned native providers to both native instrumentation and
   `vvotel`.

**Implementation:** OTLP example, dev stdout variant, View/exemplar tests and
README with ownership order.

**Application shutdown order:** stop new HTTP/gRPC/job/scheduler admission;
drain handlers, server streams, jobs and runtime passes while their database
pools remain usable; finish/close outstanding client response bodies and DB
rows, then close pools. Unregister application and `vvotel` callback handles
and wait for in-flight callbacks. Start trace and metric ForceFlush with separate
deadlines from an uncanceled shutdown context, attempt both and join their
errors; one exhausted signal budget cannot consume the other's. Finally shut
down both providers with separately budgeted calls and join every cleanup error.
The application bounds each drain step and continues cleanup after an error.
Contrib `runtime.Start` has no unregister handle and may collect through the
last metric flush; provider shutdown terminates that callback's lifetime.

**Done when:** tests inject failure after every successful constructor/ownership
transfer and prove exact cleanup counts, no leaked exporter and no double
shutdown. Buffered spans and metrics export before their normal interval.
Concurrent/repeated Shutdown returns the cached result. Blocking-until-deadline
and failing trace/metric flushes prove both are attempted within independent
budgets and both providers still shut down. Barrier races prove flush-first
waits before shutdown, shutdown-first rejects flush with `ErrTelemetryClosed`,
a canceled secondary waiter does not affect the owner, and every completed/
late Shutdown receives the identical cached result. Admission/drain/body/row/callback
fixtures pin the order; no callback runs after provider shutdown. These bounds
assume native lifecycle methods honor context deadlines; the recipe cannot
interrupt a nonconforming exporter that blocks forever. No lifecycle function
enters the published module.

### [ ] OT-D03. Provide Collector resilience and sampling configurations

1. **Mechanism and source:** the OTel Collector owns bounded queues/retries,
   optional `file_storage` WAL, batch processing, memory limiting and tail
   sampling.
2. **Why:** SDK exporter failure must not fail requests, but telemetry loss must
   be visible and memory/disk use finite.
3. **Adaptation:** provide validated development and production examples.
   Production receives OTLP, applies memory limiter and batch, exports with a
   bounded sending queue/retry and optional WAL. A separate tail-sampling
   variant retains errors, high latency and a bounded baseline. Every SDK on
   the managed path to that Collector uses `AlwaysSample`; neither
   `ParentBased(AlwaysSample())` nor a ratio sampler is sufficient because an
   unsampled remote parent can still be dropped before export. The Collector
   cannot recover any span dropped by an upstream/head sampler outside that
   managed path, so that limitation is stated with the deployment boundary.
   Decision wait, expected traces, late spans and load-balancing requirement
   are explicit. A linked durable-job retry is a separate trace and receives
   its own tail decision. Collector self-metrics and drop alerts are enabled.
   This is not an exactly-once or audit pipeline.
4. **Already present:** no Collector configuration in the repository.
5. **Top-level DX:** run the dev config locally; copy and size the production
   config from declared throughput and outage budgets.

**Implementation:** pinned Collector configs, sample environment and a syntax/
startup validation target outside offline `make check`.

**Done when:** configs start against the pinned image, reject unbounded queues,
and an unavailable sink leaves the sample business request successful while a
self-metric reports retry/drop state. A deterministic error/slow trace that the
ordinary ratio sampler head-drops, plus a trace under a remote-unsampled parent,
proves the ordinary recipe makes no tail-retention promise and the all-head-
sampled tail variant presents both candidates to the Collector.

### [ ] OT-D04. Provide useful Views, SLO queries and alerts

1. **Mechanism and source:** SDK Views choose aggregation/buckets/attribute
   filters; PromQL recording and alert rules turn stable metrics into operator
   questions.
2. **Why:** emitting metrics without tested queries still leaves every user to
   rediscover availability, latency, saturation and telemetry-loss semantics.
3. **Adaptation:** ship scope-targeted View helpers in application recipe code,
   not `vvotel`. Provide recording/alert rules for command error rate/p95,
   storage error rate/p95/stream failures, cache hit-stale-negative/eviction and
   occupancy, auth refusal shifts, health failures, runner/drain/periodic
   failures, jobs queue delay/handler failures/renew-apply/admission/scheduler
   saturation and Collector drops.
   Every ratio states its denominator and layer. Missing series remain unknown,
   never healthy zero. Default recipes retain native transport spans and metrics
   for the exact path-only full patterns `/live` and `/ready`. The net/http
   policy writes the reserved bounded metric dimension
   `vv.http.management=true` only after one of those exact registered wrappers
   executes and writes `false` on every other server datapoint. This late value
   overrides a same-key `otelhttp.Labeler` value. HTTP availability and latency
   SLO queries exclude only `true` from numerator and denominator; they never
   classify by projected `http.route`. OT-B06 probe telemetry remains included
   in its separate health rules. The metrics-only variant filters `true`
   datapoints in Collector/backend policy, preserving request spans and probe
   parentage. Native `WithFilter` resolves the handler against the same sealed
   table and suppresses a request only when the selected wrapper has the exact
   full pattern `/live` or `/ready`; redirects, host-qualified and
   method-qualified patterns do not match. It suppresses both trace and metric
   instrumentation and is documented only as an explicit both-signals exclusion
   variant, never as a metrics-only switch.
4. **Already present:** command histogram boundaries only; no query/alert
   artefacts.
5. **Top-level DX:** import/copy one rule set and change thresholds; native Views
   and backend queries remain fully editable.

**Implementation:** tested metric-name references, Prometheus-compatible rules
and a compact dashboard/query catalogue.

**Done when:** every referenced metric/attribute exists in the Frostgrove
registry, native manifest or pinned Collector self-metric contract, and
`promtool` fixtures catch a rename, reset, partial-layer mix,
empty input and zero denominator. Missing data cannot render as healthy zero.
Management-route fixtures cover default inclusion, metric-only exclusion and
both-signals exclusion: assert transport signal counts, exact HTTP SLO
denominators, authoritative marker values, unchanged probe calls/counts/
durations and the expected probe span parent (request span when retained; no
fabricated request parent when excluded). One sealed table contains exact plus
host-/method-qualified colliding patterns; selected-wrapper precedence and
same-key Labeler spoofing cannot change classification. An unmatched path or
ServeMux redirect is not a management route; an admitted outer route remains
authoritative when its handler invokes a nested ServeMux.

### [ ] OT-D05. Gate schema, local consumption and live OTLP

1. **Mechanism and source:** generated-schema check, isolated-module consumers,
   a Frostgrove wire validator and optional Weaver live-check catch different
   failures: drift, module leakage, custom-signal mismatch and upstream semconv
   mismatch.
2. **Why:** unit tests cannot prove that a tagged external consumer resolves the
   module or that actual OTLP matches the declared schema.
3. **Adaptation:** keep offline `make check-otel-schema` and
   `check-otel-module`. The pre-release `GOWORK=off` consumer uses a generated
   temporary modfile with an explicit local `replace`; it proves public API and
   dependency isolation, not publication. The remote consumer has no replace
   and is a post-publication verifier, not a pre-push release gate: it runs only
   after the complete atomic release tag set is remotely visible, including the
   nested module's exact `otel/$V` tag. It requires
   `github.com/frostgrove/vv/otel@$V` directly and must not obtain that package
   through the root `$V` tag, the checkout, `go.work`, a vendor tree, a module
   cache warmed from the checkout or any `replace`. A custom OTLP fixture validates
   missing/extra Frostgrove signals and attributes against the generated v2 wire
   manifest. Pinned Weaver validates only upstream semantic conventions; it is
   not claimed to understand Frostgrove's JSON registry. Experimental semconv
   versions and schema migrations are recorded explicitly. An application-native
   budget manifest, versioned separately from the Frostgrove registry, records
   each pinned bridge/semconv mode, instrumentation scope/version, metric
   name/type/unit, exact permitted keys and complete value domains including
   absence, configured route/RPC/pool sets and its independently authored series
   ceiling. The checker calculates the domain product for each instrument and
   requires it not to exceed that ceiling. Unknown instruments, attributes or
   values, and unexplained increases in a declared domain or ceiling fail the
   gate; widening requires an explicit reviewed manifest migration. A test-only
   scanner counts actual series per instrument/resource/scope and rejects an
   over-budget fixture, including native HTTP/gRPC/DB/runtime signals. Resource
   and scope identities are checked against the fixture's fixed configured set,
   so extra identities cannot hide series in another budget bucket.
   Before tag creation, `make version V=$V` rewrites every direct and indirect
   first-party requirement in every published module from its development
   sentinel/pseudo-version to the exact module-set `$V`, and drops every
   checkout-local first-party `replace` from published manifests. Replacements
   in the unpublished `test`/`_examples` modules remain local tooling. Release
   preflight independently rejects any first-party requirement in a published
   module that is not exactly `$V`, every remaining local sentinel and every
   `replace`; checking only the root requirement is insufficient. The complete
   discovered module tag set is still pushed atomically before remote consumer
   verification.
4. **Already present:** generator check, isolated module test and a remote
   release consumer that references only `vvotel.New`.
5. **Top-level DX:** contributors run the local gate without publishing;
   post-publication automation checks the exact remotely published nested tag.

**Implementation:** scripts, Make targets, versioned native budget manifest,
domain calculator, SDK/OTLP series scanner and structural/adversarial fixtures.
A hermetic release-choreography fixture uses a never-before-published version
and fake remote/proxy visibility to prove that local checks precede the atomic
root+nested tag publication and that the no-replace consumer follows it.
Manifest fixtures also prove `make version` removes all published sibling
sentinels/replaces, retains unpublished fixture replaces, and release refuses a
single stale direct or indirect first-party version.

**Done when:** the local-replace consumer compiles every public adapter with
`GOWORK=off`; after the full tag set is pushed, the post-publication no-replace
consumer resolves `github.com/frostgrove/vv/otel@$V` in a clean module/cache and
fails when only root `$V` exists;
the custom wire gate fails on missing/extra signals/attrs; optional upstream
Weaver/Collector validation runs when installed; offline checks stay
network-free. The base native-budget manifest, calculator and scanner run in O2
before any native integration is advertised as usable. O4 adds hostile
Host/port/route/method/status/RPC-name/extra-attribute cases and mutation controls
that remove a View filter, widen a domain or add a resource/scope; the complete
gate runs again in final verification.

### [ ] OT-D06. Replace stale OTel documentation with exact composition docs

1. **Mechanism and source:** documentation is part of Frostgrove's contract and
   follows Index → decision → use case → flow → module/guide lookup.
2. **Why:** current docs omit jobs/auth/health/runtime, claim worker emission is
   absent and name unreleased OTel versions.
3. **Adaptation:** update English/Russian module docs, spec, use case, flows,
   indexes, example README and main roadmap cross-link. Separate guaranteed
   Frostgrove schema from upstream semconv maturity. Show magic path first and
   native escape paths next. Document signal cost, sampling, lifecycle, privacy,
   transaction wording and dashboard denominators. A versioned per-library
   privacy matrix records signal/field, pinned default versus opt-in, data
   source and trust, exposure risk, keep/drop/redact policy, exact enforcement
   point and a canary test. It covers `url.full`/path/query, user agent,
   forwarded/client addresses, error events/status descriptions, SQL
   statement/parameters, prepared-statement names, collection/table names, RPC
   method/metadata, tracestate and baggage. Fiber's default `enduser.id` comes
   from a Basic Authorization username; no option at the selected pin disables
   that extraction, so the native trace export projection drops this field.
   pgx emits COPY's `db.collection.name` and Prepare's `pgx.prepare_stmt.name`
   even with SQL capture disabled; the projection drops both. Its SQL text and
   parameter fields stay disabled by constructor options, and OT-D01's scoped
   tracer delegate plus export Name projection cover SQL/table-bearing span
   names. The matrix gives each of these sources its own enforcement row and
   canary. It also includes native metric exemplar filtered attributes: View
   filtering alone does not sanitize those exported values.
   For the default recipes, keep only approved route/RPC/operation names,
   configured deployment/pool identity, closed outcomes and trace/span IDs.
   Drop native URL/query/header/metadata/SQL/table/prepared-name/user-identity/
   error-text/stack values, untrusted tracestate and all baggage. Native
   constructor options prevent capture where available; OT-D01 Views bound
   metric series. An application-owned
   `sdktrace.SpanExporter` delegate first classifies the original instrumentation
   scope, then projects span Name through that scope's fixed allowlist/fallback
   and projects attributes, events, status descriptions, links, `Resource()`
   attributes/schema URL and instrumentation scope name/version/schema URL/
   attributes into an allowlisted immutable wrapper before forwarding to OTLP.
   Because pinned `sdktrace.ReadOnlySpan` has an unexported method, that wrapper
   embeds the original interface to retain conformance but explicitly overrides
   both `InstrumentationScope()` and deprecated `InstrumentationLibrary()` to
   return the same projected scope; no promoted accessor may expose the original.
   An `sdkmetric.Exporter` delegate first classifies each
   original `ScopeMetrics.Scope`, then deep-copies `ResourceMetrics` and projects
   `ResourceMetrics.Resource` attributes/schema URL, every `ScopeMetrics.Scope`
   name/version/schema URL/attributes, datapoints and exemplar filtered
   attributes before forwarding. Approved resource keys, values and schema URLs
   are a closed configured deployment-identity domain. Approved scope
   name/version/schema-URL/attribute tuples are the fixed native manifest roster;
   unknown tuples use the manifest's bounded fallback or are dropped exactly as
   that signal's policy declares. Scope-specific field/name policy always uses the
   original classified scope, never the projected fallback. Hostile extra
   resource keys, resource values, scope names, versions and schema URLs cannot
   pass through or create an unbudgeted identity. The delegates do not mutate
   SDK-owned data and pass
   exporter lifecycle through exactly once under OT-D02 ownership. Matrix rows
   name the concrete option, View or delegate enforcing their policy; no row
   relies on an unspecified future redaction step.
   These native policies govern application export. In-process native SDK
   processors/samplers can still see captured native fields; they do not inherit
   `vvotel`'s pre-API no-error-text/no-URL guarantee. Public-edge rows separately
   test the TraceContext extraction delegate, new-root link and removed
   tracestate; trusted tracestate retention is an explicit application policy.
   Document the full admission → drain/bodies/rows/pools → callback unregister
   → independently budgeted trace/metric flush → provider shutdown order,
   including contrib runtime's provider-owned callback lifetime.
4. **Already present:** bilingual OTel module docs, [[UC-030]], [[FL-034]] and the first
   roadmap.
5. **Top-level DX:** one page gets service, storage/CRUD, cache, remote, auth,
   health, runtime/periodic and jobs/worker/scheduler telemetry running; focused
   sections show exact low-level APIs.

**Implementation:** the base privacy-matrix row, trace/metric export projection
and exported-OTLP secret/cardinality canary for each net/http, Gin, Fiber, gRPC,
HTTP-client, pgx/`database/sql` and Go-runtime recipe ship with that recipe's
D01–D02 base in O2. The span delegate projects every field available to the
downstream exporter, including Name, attributes, events, status, links,
Resource and both instrumentation-scope accessors. The metric delegate projects
ResourceMetrics, ScopeMetrics, datapoints and exemplar attributes. O3 completes
the native privacy matrix and operational composition; O4 adds hostile and
mutation variants. O5 completes all linked bilingual docs and index rows. No
recipe is declared usable before its own export policy and base canary exist.

**Done when:** every named symbol/path exists, both languages agree on defaults,
and a clean-context reviewer can assemble the example without reading source.
Per-library exported-OTLP canaries cover span Name, every matrix field, events,
status, links, resource attributes, instrumentation scope name/version/schema
URL, datapoints and exemplar filtered attributes. Hostile extra resource keys,
resource values/schema URLs and unknown scope tuples/attributes prove both
bounded fallback/drop and absence of SDK-owned-data mutation. A subprocess with
`OTEL_RESOURCE_ATTRIBUTES=secret.key=secret-value` proves environment detector
merging cannot bypass the projection. A hostile downstream span exporter calls
both `InstrumentationScope()` and deprecated `InstrumentationLibrary()` and
asserts they expose the identical projected value. They include Fiber
Basic Authorization with a secret username and pgx Query/Prepare/Batch/COPY with
secret SQL, prepared names and table identifiers; a control without the
corresponding option/View/delegate demonstrates what would leak or multiply.

## Delivery order and gates

Implementation starts only after roadmap review closes.

Open cards are delivered horizontally in three passes. A base pass must already
preserve the wrapped effect, exact call count, derived context, closed attribute
vocabulary, secret exclusion and configured cardinality bounds; those are
runtime correctness, not deferred hardening. The advanced pass adds secondary
capabilities and operational depth. The edge pass then exhausts hostile
providers, panic/Goexit, typed nils, race schedules, mutation controls and every
boundary value. A card stays unchecked until all three applicable passes close,
but an unfinished edge matrix does not hold the next subsystem's base adapter
hostage.

| Slice | Cards | Gate before next slice |
|---|---|---|
| O0 architecture | A01–A02 | corrected roadmap re-reviewed; [[D-134]]/[[UC-030]]/flow shape accepted |
| O1 foundation | A03–A06 and B01 base | `otel/` focused tests, real-SDK command proof, schema check and clean implementation review |
| O2 base integration breadth | base paths of B02, B05–B07, B09–B12 and C01–C06; executable D01 integrations for net/http, Gin, Fiber, gRPC, HTTP client, pgx/`database/sql` and Go runtime; D02 SDK; each integration's D06 export projection/privacy row; base native-budget manifest/calculator/scanner from D05 | one ordinary application can assemble every advertised integration without reading framework source; smoke tests cover success plus one representative failure, exact call/result/context preservation, explicit providers, bounded attributes and secret exclusion at exported OTLP; CRUD covers direct source, primary transaction and replica paths; jobs cover enqueue, handler, worker and scheduler paths; every native integration passes its base export canary and calculated series ceiling; local OTLP export and clean implementation review pass |
| O3 advanced capability pass | B03–B04; advanced B11 capability/transaction/native-bulk matrix; C07; D03–D05; remaining advanced D01–D02/D06 operations | stream lifetimes, aggregate callbacks, nested/savepoint/native capabilities, restart/retry propagation, full native composition, collector/rules/release workflows and clean implementation review pass |
| O4 edge and vulnerability closure | every deferred done-proof from B02–D06 | exhaustive boundary matrices, hostile provider/exporter controls, panic/Goexit/typed-nil paths, race and teardown schedules, privacy/cardinality mutations, dependency/release adversarial checks and fresh clean reviews all pass |
| O5 final consistency | every still-open card | all checkboxes close, bilingual docs/schema/history agree and complete repository verification passes |

O2 intentionally includes CRUD and durable-job integration breadth before
stream/gauge sophistication or exhaustive adversarial closure. D01/D02/D06
checkboxes remain open after their base recipes because O3 and O4 still owe the
advanced operational matrix and edge proofs. This changes delivery order, not
scope. The declared no-secret, bounded-name, explicit-provider, exact-effect and
fail-open invariants apply to every base adapter immediately.

**O2 proof (2026-09-09):** real-SDK tests cover every listed Frost adapter,
direct/transaction/replica/bulk CRUD and enqueue/handler/worker/scheduler jobs.
The native gate requires exact real rosters for HTTP, Gin, Fiber, gRPC, pgx,
`database/sql`, pgxpool and runtime, validates raw metadata before projection
and scans projected series/privacy. The production recipe composes Frost plus
two isolated exact native scopes into one app-owned OTLP pipeline. Fresh
post-fix reviews passed native budget/semconv mode and production multi-layer
composition; CRUD/jobs post-fix review passed exact contexts, exemplars and
trace-start fail-open behavior. `check-otel-schema`, `check-otel-module`,
`check-otel-live` and `check-otel-native-budget` are green.

**O3 proof (2026-09-09):** storage stream lifetime, bounded cache-memory
aggregate registration, CRUD optional/native/nested transaction capabilities
and durable restart/retry propagation pass focused race and real-SDK tests. All
59 registry signals are implemented. The app-owned native/production recipes,
five digest-pinned Collector configurations, 41 Prometheus rules, strict v2
OTLP wire fixture, hermetic root-versus-nested release proof and pinned Weaver
listener/send/report cycle pass. HTTP management classification uses an
authoritative bounded `true`/`false` metric dimension proven against Labeler
spoofing, colliding patterns and nested ServeMux mutation. Two independent
post-fix reviews found no remaining O3 critical/high/medium gap.

Every implementation slice requires a fresh reviewer with no inherited task
context. Critical/high findings are fixed and re-reviewed before the next
slice.

Each runtime card promotes every signal it begins emitting in the same change:
registry availability, append-only availability history, current source
inventory, generated Go schema and wire manifest move together after runtime
proofs. Emission from a descriptor still marked `planned` fails the slice gate.

For every O2/O3 adapter that co-emits a span and metric, the slice gate uses the
real SDK to prove that the metric exemplar carries that adapter span's exact
trace/span IDs. A paired trace-start failure must still emit the metric with the
incoming context. This applies to storage, health, authentication, remote, CRUD,
periodic and jobs producer/handler paths, not only commands.

Every slice extends an explicit inventory of newly reachable OTel methods and
the independent fault matrix before its adapter can be marked done. In addition
to existing Start/SetAttributes/SetStatus/End/Record/Add/IsRecording/AddEvent,
this includes span-context reads, int64 histogram Record, RegisterCallback,
callback ObserveInt64, Unregister, and propagation Inject/Extract/carrier
Get/Set/Keys whenever reached. Constructor errors, nil/typed-nil results,
per-method panics, signal selection, callback retention and teardown are tested
at their actual boundaries. Each control pins unchanged business effects and
the working sibling signal; A04's current proof is not reused as evidence that
a newly introduced method is already safe.

## Final verification

- [ ] `make fmt` leaves `gofmt -l` empty.
- [ ] `make unit` passes under race across every module.
- [ ] `make integration` passes twice consecutively.
- [ ] `make vet` passes across every module.
- [ ] `make examples` passes.
- [ ] `make check` passes offline.
- [ ] `make check-otel-schema` passes.
- [ ] isolated local OTel consumer passes with `GOWORK=off`.
- [ ] real SDK parent/link/exemplar/privacy suite passes.
- [ ] every pinned native-integration fixture compiles with `GOWORK=off`.
- [ ] versioned native-budget manifest, domain calculator and exported-series
  scanner pass, including adversarial and widening-mutation controls.
- [ ] optional pinned Collector/Weaver live validation passes in its declared
  environment.
- [ ] mutation probes have demonstrated that propagation, record, span-end,
  privacy and wrapper-effect tests fail when their guarded behaviour is removed.
- [ ] two final clean-context reviews find no unresolved critical/high issue.
