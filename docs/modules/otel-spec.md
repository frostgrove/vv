# OpenTelemetry signal contract

The registry at `internal/otelreg/registry.json` is the source for
`otel/schema_gen.go` and `otel/wire_manifest.json`.
`ContractVersion` is `vv-otel/v2`; `ScopeVersion` remains the
published module version. The contract identifier is not an OpenTelemetry
schema URL. The [v1 to v2 migration](../release-notes/2026-09-09-otel-v1-v2.md)
records the compatibility boundary.

[[D-128]] governs ownership; [[UC-030]] states the consumer guarantees; [[FL-034]] maps the
adapters. The registry declares future signals without implementing their
adapters. Fail-fast assembly constructs every enabled provider-compatible
metric instrument, including instruments whose adapter remains planned;
construction is not emission. Every signal states
`availability: implemented` or `planned`. Existing implementation remains
command spans/duration, storage operation spans, and cache counters/span events.

## Registry model

| Section | Contract |
|---|---|
| `domains` | Finite typed wire values; string, bool or int64 |
| `components` | Wire identity, source, operations, outcomes, arbitrary named vocabularies and the span-name domains the component owns |
| vocabulary `source` | Explicit source inventory, exclusions with reasons and provenance: enum, methods, functions, projection or registry-owned |
| vocabulary `mapping` | Total source-to-wire mapping; source spellings can differ from wire values |
| vocabulary `unknown` | Explicit drop, omit or closed fallback policy |
| `attributes` | Wire name, type, owned finite domains, source, privacy, maturity and metric eligibility |
| `attribute_sets` | Reusable finite bindings |
| `signals` | Span, span event or metric descriptor, exact declared-attribute source sets and allowed attribute variants |
| `source_shapes` | Complete typed fields, accessors, callbacks and interfaces; current AST inventories or explicit planned source contracts |
| `source_facts` | Closed numeric source facts used by field-presence predicates, separate from measurement admission |
| `migration` | Complete v1→v2 metadata, note path, nonempty unique wire changes and signal-ID ledger; copied into both generated surfaces |
| `migration.signal_ids` | Current ledger checked against independent, append-only `internal/otelreg/signal_history.json` |
| signal `availability` | Current runtime truth checked against independent `internal/otelreg/availability_history.json`; only an appended `planned` → `implemented` promotion is valid |
| `retired_signals` | Explicit retirement; historical identifiers remain reserved |
| `log_correlation` | Separate application-log keys, selected by explicit handler construction |

An enum inventory is checked against the exported constants of its named type
in the declared source file. Literal string constants and direct String-method
branches are also compared with the recorded source values. Composite
classifiers and callbacks have explicit projection inventories; future root
enums are identified as registry-owned until their neutral seams exist. Adding
a source constant requires either its mapping or an explicit exclusion.
Method inventories identify source files and interface types. Their complete
resolved method sets include embedded interfaces, aliases and generic
instantiations. Metadata methods such as Meta, Paths, Capabilities and Dialect
are explicitly excluded with reasons; silently omitting one fails generation.
Free-function inventories check every exported function in their declared file,
including explicit exclusions for constructors and options.

The 87 structured inventories comprise 77 current and ten planned surfaces.
Every member is mapped to reciprocal signal inputs and consumed attributes,
linked to a nested shape, or excluded with a nonempty privacy/semantic reason.
Boolean members may name an explicit subset of their reciprocal signals as
source-admission gates; a gate is not an emitted attribute.
Shape ownership is a closed component list; scope prose is metadata, not a
validation rule. Current shape additions, removals, renames and signature/type
changes fail the AST check. Planned storage-stream and executor-unwrapper
interfaces, lifecycle/scheduler events and C01 carrier extensions are
prospective contracts, not claims that those root APIs exist.
Their owning implementation card reconciles exact signatures and promotes the
shape to current; additive accessor inventories must merge into the full
current surface. Implemented signals cannot depend on planned source inputs.

Remote `BulkDelete` maps to `delete_many`, runtime `per-replica`
maps to `per_replica`, and jobs `cancelled`/`timed_out` map to
`canceled`/`timeout`. An arbitrary source string never passes through.

A variant binds each attribute to one finite domain, optionally narrowed to a
constant in that domain, or an
explicitly admitted declaration. It can allow omission; explicit absence and
presence cannot conflict. Attributes not listed in the variant are forbidden.
Base bindings cannot be silently overridden. Spans additionally state status;
metric variants admit only metric-eligible finite attributes.
Both domain references and singleton constants must belong to the attribute's
owned domains. A syntactically valid value from a different vocabulary is not
enough.
Ownership is resolved per variant from its exact component value. Operations,
outcomes and component vocabularies must use that component's precise domain;
only explicitly global error domains are shared. Combined cache signals can
therefore admit facade/backend variants without borrowing another component's
operation or outcome domain.
Every finite span-name domain is owned by exactly one component and every span
using it belongs to that component. Foreign, duplicate, unowned and unused
ownership fails generation.

Declared resource names remain trace-only. `ApprovedName` is the explicit
caller-approval boundary used by `Config.ResourceName`,
`WithServiceResource` and `WithStorageResource`; untyped literals remain terse,
while dynamic strings go through `ApproveName` or `MustApproveName`. A direct
conversion is the low-level escape hatch, but runtime admission still rejects
an invalid value. The registry permits 64 bytes and Unicode letters/digits plus
dot, underscore and hyphen. One `Telemetry` admits at most 32 distinct names
across service and storage adapters; duplicates are free, and a configured
default consumes no slot until a trace adapter binds it. Invalid or excess
names are omitted. Names never enter metric attributes or exemplar filtered
attributes. `MaxResourceNameBytes` and `ValidResourceName` are generated from
the same declaration. The mutable legacy metadata maps are informational
projections, not runtime authority.

## Components and signals

| Component | Observation boundary |
|---|---|
| command | Ten commands, including optional restore; metadata methods forwarded |
| storage | Nine Store methods; Open ends when the reader is returned |
| storage_stream | Consume from first Read or Close through EOF, error, close or unwrap |
| cache | Facade events, reason, memoization, items, encoded and payload bytes |
| cache_backend | Memory-backend events, reason, items, value and charged bytes |
| cache_memory | Fixed aggregate Stats registration; no operation or instance label |
| crud_source | Direct calls and exact optional transaction/native-bulk effects |
| remote | Eight logical calls, separate from native transport spans |
| authentication | Complete authenticator chain |
| auth_refusal | Five refusal reasons; no credential, detail or cause |
| health | Actual enabled probes; importance and state, no additional probing |
| runtime_lifecycle | Actual transitions and completed run/drain operations |
| runtime_periodic | One pass, not a process-lifetime span |
| jobs_propagation | Injection/extraction and bounded trace degradation |
| jobs_enqueue | Four enqueue forms; staging never asserts commit |
| jobs_handler | Returned handler invocation, queue delay and numeric attempt ordinal |
| jobs_worker | Control-plane operations, items, bytes, admission and delivery results |
| jobs_scheduler | RunDue cycle and numeric due/placed/existing/conflict distributions |

The schema contains 18 components, 59 signal descriptors and 45 metrics.
Every metric states the exact native API kind, instrument, number type, unit,
measurement bound and independent histogram boundaries. Duration uses seconds;
byte measurements use By; closed counts use annotated units such as {item}.
There is no implicit shared histogram bucket selection.

Command success has outcome ok and omits both error.type and vv.error.code. Ordinary errors have
outcome error and a closed error type. Cancellation and timeout retain their
own outcomes/types; panic is error/panic. Authentication refusal is separately
refused without inventing an error type. Disabled health contributions and the
runtime constructor-only idle state emit nothing.

All eleven span wrappers also declare an A04 span-only terminal for
runtime.Goexit: outcome goroutine_exit, Error status, and absent error.type
(also absent vv.error.code). No duration/counter sample is recorded because
the downstream operation did not return. This covers command, storage
operation/stream, CRUD source/transaction, remote, authenticator, enabled health
probe, periodic pass, direct/staged enqueue and handler spans. It does not add
an outcome to metric-only observer contracts, change existing metric bounds,
or claim planned adapters already implement the A04 defer guard. The current
command and storage adapters do implement it through the shared operation
recorder.

The old `vv.cache.operations` counter keeps its existing facade/backend
attribute sets and is sourced by both `cache.Observer.Observe` and
`cachememory.Observer.Observe`. Rich reasons and memoization belong to the new
`vv.cache.events` counter and distribution instruments. Existing
`cache_backend`, `memory_backend`, `cache.event` and
`cache_backend.event` wire values remain unchanged.

New cache event/item variants admit exactly 31 facade and 18 backend tuples.
Memoized=true is possible only for lookup; Forget only reports deleted without
reason or error/backend. Facade encoded/payload bounds are 14/6; backend
value/charged bounds are 13/11. Byte distributions admit non-negative measured
values only when their tuple and source facts establish field presence.
EncodedBytes > 0 distinguishes an envelope-bearing lookup/load from an absent
lookup or uncached fast path; it also proves that an empty String/Bytes payload
is a present zero. Lookup/miss does not measure payload, because schema-mismatch
and decoded-expiry miss shapes do not prove consistent payload presence.
Successful lookup_many emits an explicit aggregate, including zero. Backend
stored/hit/deleted values may genuinely have zero bytes; absent get/miss sizes
are excluded. Empty reset/close aggregate charged size is also a present zero.
Aggregate memory occupancy and capacity use `Backend.Stats`; `Stats.Closed`
gates entries, bytes and both limits so a closed backend contributes only to
the closed count.
`record_when` governs the measured number; `when` governs source-field presence.
Neither fact nor measured value contributes a metric label or cardinality.
The legacy optional cache span events keep their v1 attribute shapes; rich
reason/memoization is currently contracted by the new counter/distributions,
not silently added to existing implemented events.

Storage operation bytes (signal ID 5) measure successful persisted size only:
Put's returned Info.Size or Stage's returned Staged.Info.Size. Only put/stage
with outcome ok are admitted. The decorator does not wrap the input reader,
infer consumed bytes or report failed writes as persisted data. Stream byte
accounting is a separate signal and ownership boundary.
ID 58, `vv.storage.cleanup.removed`, records successful CleanupResult.Removed
in 0..storage.MaxCleanupLimit, with the closed More boolean (two series).
ID 59, `vv.jobs.worker.recovery.released`, records confirmed successful recover
Released in 0..`jobs.MaxReclaimBatch` (1000) with More (three series:
complete/false, complete/true, empty/false).
Worker Active and Limit remain explicitly excluded: event-sampled scheduler
snapshots are neither current gauges nor a safe aggregate capacity measure.
Open/Head/Promote metadata and all storage options/capabilities/keys/links are
forwarded; no extra object-size analytics or bearer/identity fields are emitted.

Authentication and remote durations are named `vv.authentication.duration`
and `vv.remote.duration`. Worker admission admits eight outcome/signal pairs;
delivery results admit 13 operation/mutation/control tuples (renew cannot
report applied/terminated). Run/drain failures are runtime-only. Duration
observations exclude start, admission and pre-call saturation events.
Worker event projections always consume the outer Operation and Outcome;
items/bytes also consume Failure, delivery-result samples consume Results, and
every worker signal consumes the `WorkerObserver.Observe` call. Their numeric
ceilings are 1000 items, `jobs.MaxClaimBytes` (67108864) bytes, 1–256 delivery
result items and `jobs.MaxReclaimBatch` (1000) released leases. Each scheduler
result field is bounded by `jobs.MaxDefinitions` (4096).
Worker elapsed also requires a positive source sample: zero can represent a
clock failure and is omitted until the neutral event can express presence.
EnqueueOnce uses its public closed return enum; successful transaction forms
always emit staged without inspecting internal placement/Staged outcomes.
Handler queue delay and attempt ordinal are associated with the
`AdapterHandler` invocation that supplies `DeliveryMeta`; attempt is in
1..`jobs.MaxAttemptOrdinal` (4129). The 42 command/disposition/reason tuples describe apply calls, not confirmed
mutations. Their planned OT-C05 projection uses disposition.Reason when the
disposition is nonzero, command.Reason otherwise. The current root emitter
still uses command.Reason unconditionally; C05 must fix that seam before
enabling this signal. No root behavior changes in this registry migration.

The current `runtime.Observer.Observed` callback supplies transitions only.
Replayable `RunnerState.Err`, `StartedAt` and `EndedAt` cannot prove one unique
run/drain completion and are explicitly excluded. Planned
`LifecycleObserver.ObservedLifecycle` is the sole source for operation counts
and durations.

## Cardinality

Bounds are calculated from the union of expanded finite attribute tuples.
An absent attribute is distinct from any present value. Overlapping variants
are deduplicated. Constants contribute one choice; a boolean contributes two;
an optional field adds an absent alternative. The generator refuses an
expansion or union exceeding 200,000 tuples. Every metric requires a positive
authored series_budget. It is only a ceiling and is never copied into
calculated metadata; both values are available in the manifest and rich Go
descriptors. Most budgets deliberately leave room above the current bound.

The existing command bound is 10 operations times ten result shapes: one
success, seven ordinary error/panic types, cancellation and timeout, giving
100. The existing cache bound is 6 facade operations times 10 outcomes plus
7 backend operations times 8 outcomes, giving 116. Layer and component are
correlated constants, not independent multipliers.

These are bounds on instrumentation attribute sets per instrument/resource/
scope. Application Resource multiplicity, SDK aggregation and histogram
export buckets are separate concerns. Numeric measurements do not multiply
attribute cardinality.

## Stable selection and generated API

The v2 roster assigns stable integer IDs 1 through 59. IDs are not bit
positions and are never assigned by sorting. The generated ID type is
`Signal uint16`; `Signals` is a closed list (`[]Signal`), not a machine-word
mask. The representable ID range is 1..65535, with zero reserved as invalid.
The separate embedded history freezes every initial name/ID. Renaming,
deleting or reusing a signal in both registry and migration ledger still
fails against that history. Explicit retirement keeps the historical ledger
entry; new signals use never-assigned IDs. Each `Signal…` constant is typed;
`AllSignals()` returns a fresh copy and `Signal.Valid()` recognizes only
currently registered IDs. Provider requirements are tracer, meter or
context_only. `New` takes one copy-safe descriptor snapshot, orders it by
numeric signal ID and builds a private active set. Its constructor contract is
`Disable: vvotel.Signals{vvotel.SignalX, ...}`: the caller's slice is copied,
and unknown or duplicate explicit IDs fail before master-disabled or provider
handling. Legacy aliases union idempotently only after that validation:
command traces disable ID 2, command metrics ID 1, storage traces IDs 4 and 8,
and cache metrics IDs 9 and 12–23 while leaving context-only events 10 and 11
active. An empty list disables nothing. No exported mutable group slices exist.

`Disabled` returns a no-op handle without touching either provider,
constructing an instrument, registering a callback, starting work or changing
a global. An enabled config with neither provider is invalid even when every
signal is listed in `Disable`. With one provider, only that family plus
context-only signals is active. A provider getter runs only when an active
signal needs its family; when both run, tracer precedes meter.

The meter path constructs the 45 enabled descriptors in increasing signal-ID
order: 14 float64 histograms, 13 int64 histograms, 12 int64 counters and six
int64 observable gauges. Description, unit and per-signal histogram boundaries
come from the descriptor. Observable gauges are constructed without callbacks;
backend registration remains a separate fallible B04 operation. Planned means
"not emitted by an adapter", not "skipped during assembly".

Provider nil/panic and instrument error/panic/nil failures return a redacted
`*AssemblyError`. `Signal`, `Provider` and `SignalName` expose only the closed
assembly location. `errors.Is` classifies the stable sentinels. Only an error
returned by an instrument constructor is available through `Unwrap`; panic
objects are discarded and never formatted. Assembly stops at the failing
constructor and performs no rollback because it owns no registrations or
lifecycle resource. `Must` panics with that exact typed error.

Existing exported constants, mapping helpers, `AttributeMetadata` and
`MetricMetadata` layouts, and both metadata maps retain their v1 shapes.
`SignalDescriptors()` returns fresh richer descriptors, descriptions,
per-instrument boundaries and numeric limits, resolved Names and
closed variant/attribute descriptors. Single-name span constants contain the
exact resolved span name. Multi-name spans have closed generated lookups such
as `SpanCommandName`, alongside the unchanged permissive v1
`CommandSpanName`/`StorageSpanName` helpers. Unknown lookup inputs fail closed.
`SignalDescriptor.Accepts(status, attributes, facts...)` checks the exact status and
attribute variant, rejecting extra/duplicate keys, wrong types, unknown values
and invalid declarations. Adapters can use the generated internal matcher
without copying subsystem-specific unions. Current command, storage and cache
emitters route their complete candidate sets through one internal admission
function before `Start`, `SetAttributes`, `Record`, `Add` or `AddEvent`; a
rejected set reaches no emitting API. The admission result is a fresh slice.
Facts are generated typed IDs with
int64 values; unknown/duplicate/undeclared facts fail closed, and required
presence predicates cannot be satisfied by omission. `AcceptsInt64` compares
exact `MinimumInt64`/`MaximumInt64` limits without a float conversion;
`AcceptsFloat64` rejects non-finite values and checks floating-point limits.
The compatibility `AcceptsValue(float64)` path rejects integer values outside
the lossless float range. `HasMinimum`/`HasMaximum` distinguish an absent limit
from zero, while the original `Minimum`/`Maximum` fields retain their float
projection for descriptor compatibility. All three admission methods enforce
`record_when` and authored bounds;
both tuple/presence and numeric admission must succeed. Returned slices do not alias its
authority. Distinct declared-name admission remains a constructor concern;
the matcher validates each individual name, not a process-global name count.
`Metric…Boundaries()` returns a fresh boundary slice for each histogram.
The private legacy `defaultDurationBoundaries` remains tied to command
duration.

Log correlation is deliberately separate from OpenTelemetry attributes:
trace_id, span_id and trace_flags belong to an explicitly constructed
context-only slog handler. They have registry-owned exported key constants,
exact lower_hex lengths 32/16/2 and preserve-record collision policy. They are
not metric labels, are not Config.Disable selections and require no Telemetry
instance. [[D-128]] specifies the handler behavior; this registry step only
declares its keys.

## Generation and checks

Run:

```sh
go run ./cmd/vv-otel-gen -registry internal/otelreg/registry.json \
  -out otel/schema_gen.go -manifest otel/wire_manifest.json
make check-otel-schema
make check-otel-live
```

Check mode compares both artifacts and writes neither. Version generation
updates the registry scope and regenerates both outputs. JSON parsing rejects
unknown fields, duplicate keys at any nesting depth and trailing documents.
`make generate` explicitly runs the OTel satellite's generation directive.
`go generate ./otel/...` from the root, or `go generate ./...` from `otel`,
writes schema_gen.go and wire_manifest.json in the satellite itself.

`make check-otel-live` runs the `oteltelemetry` package inside the unpublished
`test/` module with `GOWORK=off` and the race detector. It uses isolated real trace/metric SDK
providers, a manual metric reader and an in-process gRPC OTLP trace/metric
receiver. It opens no external connection and mutates no OTel global. The target
is an explicit pre-release proof, not part of offline `make check`. OT-D05 will
extend live validation with pinned Weaver; Weaver is not claimed to understand
this JSON wire format.

The wire manifest records scope, complete migration metadata, descriptors,
resolved variants, omission rules, signal IDs and calculated bounds. Additional
attributes are forbidden.
Generator tests compare it to the registry; satellite tests compare it to
generated Go and validate every currently emitted metric, span and span event,
including negative controls for extra attributes and unknown operations.
Future adapter tests must use the same manifest. This is Frostgrove's custom
wire contract, not a claim that Weaver understands this JSON format.

IDs, credentials, tenant/user/job identity, statements, keys, URLs, headers,
payloads, arbitrary error text and stacks are excluded from attributes created
by vvotel. Application-owned Resource and existing slog attributes remain the
application's policy.
