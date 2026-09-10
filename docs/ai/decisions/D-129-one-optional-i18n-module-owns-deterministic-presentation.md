# D-129 — One optional i18n module owns deterministic presentation

**Status:** accepted
**Implementation:** the core package, offline command and framework integration
seams described here are implemented in the current tree. External catalogue
distribution and watching, tenant authorization, durable delivery policy and
telemetry export remain application-owned.
**Invariant:** `github.com/frostgrove/vv/i18n` is the only Frostgrove
internationalization module. It owns one versioned MessageFormat profile,
locale matching, formatting, immutable catalogues and their offline toolchain.
The root module and every other published module remain free of its engine and
locale-data dependencies; transports retain ownership of protocol projection.

## The decision

### One optional dependency decision

Full internationalization is a nested module because correct plural selection,
language matching and cultural formatting require a maintained Unicode data
stack. A consumer that does not import it neither downloads nor compiles that
stack. The runtime has one public package; its command may live below `cmd`, but
there is no second runtime facade and no package for an intersection with a
transport or another subsystem.

The first grammar profile is `frostgrove-mf2/v1`. Its engine, locale-data model and
time-zone-data model are explicit compatibility identities. Supported syntax is
validated before a snapshot becomes usable; unknown functions, options,
selector keys and option values are refusals rather than best-effort output.
Changing the engine or widening the grammar is a profile migration with a
shared corpus, not an implementation detail hidden behind the same version.

This is the condition anticipated by [[D-048]]: the stdlib error catalogue is
still sufficient for simple wording, while a second presentation use case now
needs plural/select grammar, exact decimal selection, dates, money, rich parts,
typed deferred messages and reproducible releases. Those capabilities do not
move into `errs`; they earn the optional module.

### A catalogue is a declaration and then an immutable snapshot

Modules contribute ordinary message declarations: stable qualified identity,
translator description, contract revision, typed arguments, output kind,
override permission and complete translations. Importing a declaration starts
nothing and registers nothing. The application assembles the set explicitly and
construction validates the whole effective catalogue before serving or working.
An approved translation and every reviewed override pin a source digest over
the profile, canonical source locale, resolved key, render contract, source
wording and translator description. Changing source locale, wording or context
therefore makes the review stale even when typed arguments remain compatible;
that source identity is deliberately separate from the typed `ContractRef`.

Module, application and tenant wording are strictly ordered layers. An override
replaces one whole message in one locale and may change no schema, visibility,
machine error code or permission. Application wording may complete a required
locale. A tenant layer may be created only from an application-or-module
snapshot; one tenant snapshot can never be the parent of another tenant.
Snapshots and operation views are immutable values, so a request observes one
revision even while another candidate is compiled or activated.

Locale resolution and message fallback are separate. Resolution consumes
bounded, ordered choices and returns the supported locale and provenance it
actually selected. Lookup then follows a finite validated locale graph and
reports the locale and layer that supplied the template. Message locale,
formatting locale, time zone and presentation policy are separate inputs. No
process-global locale, environment fallback or host-local default participates.

Deferred messages carry a known identity, the contract they were bound against
and copied typed arguments. They render only through an explicit view. They are
not errors, strings, durable job records or a cross-language wire format.
Generated helpers pin the expected contract at application assembly, so an old
helper refuses the new snapshot before first traffic rather than at its first
message.

Generated helpers remain the production fast path. A hand-written composition
may instead build the same typed definition from a struct: construction reflects
once, validates the full direct-field mapping and compiles an encoder. It does
not add registration, package scanning or first-render compilation. Optional and
nullable topology stays explicit, and the accepted Go field shapes remain the
same closed exact argument model rather than accepting native-width integers,
floats or arbitrary structural aliases. The constructor bounds and parses the
entire Go struct tag before mapping, so malformed syntax and duplicate `i18n`
keys cannot silently enable automatic matching. Descriptor-derived contract
references fail closed on malformed or package-hard-oversized keys before
digest work.

Standalone cultural formatting does not require a synthetic message or a
catalogue. An immutable formatter pins locale, zone, capabilities,
presentation, limits and observation, and may validate a bounded registry of
named policies once. Views can derive the same formatter boundary. Scalar and
range number/money/percent/unit/date/time operations, cardinal/ordinal plural
and plural-range selection, list, relative time, duration and display names all
retain exact values, cancellation, safe parts, bidi isolation and output
preflight. Range parts preserve start/shared/end provenance; missing named
formats are explicit rather than ambient lookup fallback. Named material is
byte/item bounded before allocation, retained strings are copied, and each named
call has one terminal observation even when lookup or cancellation fails. Digit
bounds may preserve either Intl default independently. Relative time deliberately
accepts whole `int64` units only through `±(2^53-1)`; the pinned backend converts
them to exact `float64` values after that guard, while fractional and out-of-range
inputs remain unsupported.

Plain output is untrusted plain text. Rich output is a bounded structural part
sequence whose element names were allowlisted by the declaration; it is never
trusted HTML. Parts retain their MF2 type, direction and optional opaque
literal `u:id`; exact number/date parts expose bounded copied Intl subparts.
Universal options whose values are variables remain outside this profile until
the pinned engine can resolve Frostgrove's exact typed values without semantic
loss. Interpolated values are directionally isolated by default, and
untrusted directional controls are refused. Every accepted catalogue, argument,
fallback walk, rendered output and explanation is bounded before expensive work
or allocation attributable to the runtime.

### The root error and transport contracts do not move

`errs.MessageSource` remains the dependency-neutral wording seam. The optional
module adapts a snapshot to that seam; `errs` does not import the module and its
small flat catalogue remains supported. Localization changes human wording
only. Kind, code, public path, partial marker, HTTP status, gRPC code and
internal-error redaction remain transport-owned.

An application may either build the legacy locale-taking adapter directly from
a snapshot or compile an `ErrorPlan` and bind it to an operation's existing
`View`. The latter makes one resolution, formatting locale, zone and presentation
authoritative for both application messages and errors; a transport's requested
locale parameter cannot trigger a second negotiation.

Generated CRUD resources install their path hops and operation context before a
failure crosses the error boundary. A resource can add standard rendering
options declaratively; a process error policy can cover generated and
hand-written routes; wholesale renderer replacement still owns its response
shape. HTTP and gRPC bindings carry locale inputs and project the existing error
contract, but they do not parse MessageFormat or import i18n types.

Tenancy, authentication, jobs, cache and telemetry remain separate authority and
lifecycle decisions. The application performs admission before it reads private
preferences or selects a tenant overlay. A snapshot reference or retention pin
is never authorization. Cache keys include every output-affecting input, while
shared work inherits no caller identity. The runtime observer exposes only
closed outcomes, reasons, durations and bounded counts; it contains no message
identity, text, argument, raw header, path, principal or tenant value.

### Compilation, publication and delayed rendering are application-owned

A canonical compiled artefact records exact format/profile/data identities and
the semantic snapshot digest. Loading is offline, bounded and strict about
invalid UTF-8, duplicate members, depth, member count, unknown fields and
trailing data. It performs no network lookup and never widens a loader limit from
untrusted metadata.

The offline command has one operator-owned `frostgrove.i18n.limits/v1` policy
instead of command-specific widening switches. Catalogue, source artifact,
compiled artifact and derived per-file output ceilings remain distinct. Every
command accepts that policy; all catalogue-source consumers decode beneath its
catalogue and source ceilings, merge and compile receive the same catalogue
ceiling, and each output is checked against its matching class. Invalid policy
is terminal rather than an invitation to continue with defaults. Usage-manifest
and Go-source hostile-input bounds remain owned by those document/extraction
contracts instead of being mislabeled as catalogue bounds.

Offline usage extraction derives the effective graph and compiled files from
the local Go tool, then type-checks selected local packages with exact export
data. It honors persisted Go environment, workspaces, nested modules,
replacements, vendoring and overlays without downloading dependencies or
updating the checkout. A package/dependency/type/load error, cgo, a partial file
root, conflicting evidence, or a reachable external dependency that could hide
an i18n call turns requested completeness into an incomplete manifest; missing
visibility never becomes a false unused claim. Build-excluded candidates remain
hashed evidence but do not invalidate a proof about that exact effective build.
The usage document is evidence consumed by `check`, not a catalogue consumed by
`review`. A separate loss-aware merge combines newly authored source with the
previous reviewed source and refuses to discard obsolete work without explicit
intent. Usage v4 adds reproducible call-site locations, canonical file and
metadata ledgers, effective environment and package-graph identity, recomputable
source and manifest digests, and bidirectional aggregate/occurrence evidence.
Readers accept v1-v3 documents only as incomplete evidence, so legacy input
cannot assert non-use. V4 digest validation establishes internal consistency,
not authenticity or filesystem freshness; trusted release automation reruns
`extract -check` against the physical checkout before consuming non-use proof.

Publication validates a complete candidate before one compare-and-swap. The
expected head includes opaque activation identity rather than revision and
digest alone, preventing an old publisher from succeeding after A → B → A
rollback. A failed or stale activation leaves the last known good snapshot
unchanged. Retained revisions and finite pin leases are bounded and pruned by
explicit calls; constructors create no watcher, timer or goroutine. Storage,
polling, fleet rollout, readiness and durable lease persistence belong to the
application and their existing seams.

A delayed operation chooses current or pinned presentation explicitly. The
application owns the durable intent and restores and authorizes its recipient
before selecting a snapshot or locale. Public TypeScript output describes only
declared public identities and argument shapes; it does not claim a JavaScript
runtime can format the full Go profile. Cross-runtime formatting requires its
own verified subset and parity corpus.

Generational public export binds its two fixed output bodies twice: each file
has a bounded byte count and digest, and the pointer's public address is
recomputed from the exact canonical manifest and TypeScript bytes. Generation
identity separately frames that address plus file roles, names and contents.
An exported pure address helper lets independent Go consumers verify the same
protocol without relying on publisher internals. Publisher and readers walk
from the filesystem or volume root through stable descriptors, reject links and
identity changes between inspection and opening, and cannot be redirected by a
later ancestor replacement. A missing final publication root is created and
its parent synced through the pinned parent descriptor. Readers bound directory
entries and stop bounded file reads after cancellation.

## Alternatives rejected

- Expanding `errs.Messages` into an ICU runtime would make every root consumer
  pay for an optional ecosystem and would mix machine failure classification
  with presentation.
- A cardinal-only template dialect was smaller, but it would make ordinal,
  arbitrary select, nested selectors and rich structured output an immediate
  incompatible migration. The selected MF2 engine passed the required corpus
  and is bounded by Frostgrove validation rather than exposed raw.
- A Frostgrove-authored ICU or MF2 interpreter would create a Unicode semantics
  project inside the framework and still need independent locale data.
- `i18nhttp`, `i18ngrpc`, `i18ntenancy`, `i18njobs`, `i18notel` and similar
  packages would encode the extension matrix in the package tree. Ordinary
  values and existing seams already compose those axes.
- Global registries, package scanning, mutable localizers and ambient locale
  setters make tests and concurrent requests order-dependent and hide lifecycle
  from the composition root.
- Runtime translation-service or filesystem watching would add network,
  retries, goroutines and shutdown ownership to a deterministic renderer.

## What it forbids

- No import of `i18n` from the root or another published Frostgrove module.
- No second message grammar, automatic grammar detection or silent unsupported
  option fallback under `frostgrove-mf2/v1`.
- No implicit package registration, environment locale, default time zone,
  filesystem scan, network load, watcher or goroutine.
- No tenant identity in a snapshot and no overlay built on another tenant
  overlay.
- No translation changing code, kind, status, public path, redaction or wire
  envelope shape.
- No translated string treated as HTML and no unbounded formatter work before a
  limit is checked.
- No text, arguments, message keys, raw headers, paths, causes or identities in
  default observations.
- No pin, catalogue revision or locale preference treated as authentication or
  authorization.
- No private message in frontend output and no JavaScript runtime parity claim
  without a tested profile.

## Proven by

- The i18n dependency and import-lifecycle gates walk every published module and
  the selected MessageFormat/CLDR ecosystem.
- The catalogue, locale, rendering, overlay and error-source suites cover exact
  integers and decimals, cardinal/ordinal/select grammar, fallback provenance,
  dates/money/zones, rich parts, bidi isolation, hostile limits, immutable
  copies, concurrent reads and observer panic isolation under the race detector.
- The CRUD HTTP triplet and gRPC suites walk every generated operation through
  resource and process rendering, including generated path hops, manually
  registered handlers, router failures and unary/stream error boundaries.
- Artefact, controller, authoring, code-generation, standalone-consumer and
  cross-module integration suites cover the implemented lifecycle and release
  contracts. `TestCLILimitsApplyTheSourceBudgetToEverySourceConsumer`,
  `TestCLILimitsWidenCatalogDecodeMergeAndCompileTogether` and
  `TestCLILimitsKeepSourceCompiledAndDerivedOutputBudgetsIndependent` pin the
  command's operator ceilings.
  `TestStructDefinitionStrictlyParsesEveryStructTagBeforeMapping` and
  `TestDescriptorContractRefRejectsInvalidKeysBeforeDigest` pin reflective
  fail-early boundaries. External distribution, durable coordination and
  telemetry remain application integration work rather than hidden module
  behavior.

## See also

[[D-021]] [[D-033]] [[D-045]] [[D-048]] [[D-051]] [[D-058]] [[D-084]]
[[D-128]] [[D-116]] [[UC-015]] [[UC-033]] [[FL-011]] [[FL-013]]
[[FL-015]] [[FL-038]]
