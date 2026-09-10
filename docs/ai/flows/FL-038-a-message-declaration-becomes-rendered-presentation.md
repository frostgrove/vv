# FL-038 — A message declaration becomes rendered presentation

**Entry points:** `i18n.New` / `i18n.Compile` (assembly),
`Snapshot.Resolve` / `Snapshot.View` (operation policy), `Snapshot.Bind` /
`Definition.Bind` / `DefineStruct` (deferred intent), `View.Render` /
`NewFormatter` / `View.Formatter` (presentation),
`Snapshot.ErrorMessages` / `Snapshot.ErrorPlan` / `View.ErrorMessages`
(the existing error seam), `Controller.Activate`
(publication), `vv-i18n` (offline authoring)
**Implements:** [[UC-033]]
**Governed by:** [[D-129]] [[D-033]] [[D-048]] [[D-084]] [[D-116]]

This is the whole path from module-owned source wording and a typed schema to
one immutable rendered result. It includes source/review identity, strict
compile/load, locale choice, template fallback, exact values, rich/bidi output,
error projection and bounded publication. It ends before storage, a translation
service, tenant authorization, durable job policy, HTTP caching or telemetry
export: those are application boundaries rather than hidden i18n runtime work.

## Declaration and review

`i18n/catalog.go`, `i18n/source.go`, `i18n/check.go`:

1. A bounded context contributes an ordinary `Module` containing
   `MessageSpec` values. `Qualify(module, id)` is the stable key. The
   declaration also carries revision, source wording, translator description,
   closed argument schema, plain/rich output, markup allowlist, override policy
   and public-export bit. Nothing registers at import time.
2. `CatalogSpec` combines those values with source/default/supported/required
   locales, explicit parent edges, matching policy, capabilities, default zone,
   optional application overrides, limits and observer.
3. `ExpectedSourceDigestForLocale` hashes the profile, canonical source locale,
   qualified key, typed contract, source wording and translator description.
   `ExpectedReviewDigest` then hashes that identity with canonical translation
   locale and translated text. A source-locale, wording or context edit therefore
   invalidates approval independently of contract drift.
4. `EncodeSource` writes the canonical `frostgrove.i18n.source/v1` document.
   `DecodeSource` scans before decoding and rejects excessive bytes/depth/items,
   duplicate or unknown members, invalid UTF-8, trailing data and invalid closed
   enum values. It canonicalizes order and locale spelling without inventing a
   review state.
5. `MergeSource` treats a newly authored declaration as authoritative while
   carrying forward structurally eligible translations and application
   overrides absent from it. Stale, review-required and rejected identities are
   preserved verbatim for `Check`, never promoted. Source-locale changes and
   obsolete authoring work are refused unless pruning was explicitly requested;
   source/contract/review identities are not silently restamped.
6. `Check` validates source while retaining authoring states. It classifies
   missing, stale, review-required, rejected, unused and structurally invalid
   entries, calculates exact coverage and bounds retained findings. A complete
   usage manifest makes absence usable as evidence; an incomplete one does not.

## Construction and the pinned profile

`i18n/catalog.go`, `i18n/numeric.go`, `i18n/datetime.go`,
`i18n/formatlocale.go`, `i18n/work.go`, `i18n/problem.go`:

1. `New` preflights aggregate cardinality and bytes before deep work. It
   validates the one public profile `frostgrove-mf2/v1`, its capabilities, locale
   graph, message identities, schemas, review digests, required locales and
   effective application overrides.
   `New` and `Overlay` use the exact snapshot-material accounting recorded by
   `Encode` and enforced by `Load`, including equality at configured ceilings.
   An aggregated `*Problems` always matches `ErrInvalidCatalog`; the presence
   of any `ProblemLimit` item also makes it match `ErrLimitExceeded`.
2. The profile accepts `number`, `integer`, `currency`, `percent`, `offset` and `string`.
   `date`/`time`/`datetime` and `unit` require their declared capabilities.
   Function operands are typed variables; option names and values are closed;
   literal `u:id` and `u:dir` carry structured-result metadata. Rich markup may
   carry literal `u:id`; other markup options/attributes are outside the profile.
3. Template validation proves selector shape, required wildcard, locale plural
   categories, local-reference depth, declarations/selectors/variants,
   conservative expansion, markup balance and formatter support before the
   compiled record is admitted.
4. Integer values retain signed/unsigned/big-integer precision. A decimal keeps
   its lexical visible-fraction semantics. Date and number option resolution is
   checked against the requested formatting data rather than silently accepting
   an upstream fallback.
5. Every problem is bounded and sorted. Failure returns no `Snapshot`.
   Successful records, descriptors, locale policy and caller material are
   cloned into one immutable, deterministic revision/digest identity.

## Source becomes a runtime artifact

`i18n/artifact.go`:

1. `Compile` calls the same construction path and emits canonical
   `frostgrove.i18n.catalog/v1` JSON containing exact grammar, engine,
   locale-data and time-zone-data-model identities plus semantic digest.
   `Encode` does the same from an already validated snapshot.
2. `Loader.Load` or `LoadFS` reads beneath local artifact/catalog ceilings,
   rejects invalid UTF-8, duplicates, unknown members and trailing data, and
   reconstructs the snapshot without network or ambient state.
3. The loader recompiles the representation and requires byte equality. An
   artifact that is semantically similar but non-canonical, incompatible with
   the pinned identities, or trying to widen local limits is refused.
4. `LoadFS` owns only the opened file and closes it on success and failure. No
   constructor starts a watcher, timer or goroutine.

## A preference becomes a sealed resolution

`i18n/locale.go`, `i18n/catalog.go`:

1. The application orders `Exact` and `AcceptLanguage` choices from the closed
   sources explicit, user, protocol, tenant and application. Choice constructors
   copy input and perform early representation bounds.
2. `Resolver.ResolveContext` enforces choice/header/range/tag limits, parses
   weights, `q=0` exclusions and wildcard, and walks exact/lookup or opt-in
   best-fit policy. Malformed, excluded, unsupported and limited inputs remain
   distinct terminal reasons.
3. The result contains only canonical supported locale, source and closed
   outcome/reason plus bounded preference steps. It never retains the raw
   header. Default-on-miss is explicit and reported as application policy.
4. A private issuer seals each `Resolution`. `Snapshot.View` requires a matched,
   internally consistent result issued by its own resolver, so a struct literal
   or another catalogue cannot smuggle in a supported-looking locale.

## A resolution and message become output

`i18n/value.go`, `i18n/optional.go`, `i18n/struct_definition.go`,
`i18n/render.go`, `i18n/formatter.go`, `i18n/formatters.go`,
`i18n/bounded_text.go`:

1. `Snapshot.View` pins resolution, formatting locale, time zone and
   presentation. Formatting defaults to the resolved locale; an empty view zone
   inherits the catalogue default, which itself defaults to UTC. A non-UTC zone
   requires a caller-declared time-zone-data version.
2. `Snapshot.Bind` checks one known key against supplied `Text`, `Bool`, signed,
   unsigned, big-integer, decimal, money, calendar-date, instant, enum and null
   values. It distinguishes absent/null/present, rejects extra/duplicate/wrong
   types and copies mutable caller values into a deferred `Message`.
3. `Define` additionally compares a literal `ContractRef` before returning a
   `Definition[A]`. Generated helpers therefore fail during assembly when their
   key/revision/digest no longer matches the selected snapshot.
4. `DefineStruct` and `NewStructDefinition` provide the same typed shape for
   hand-written composition. Construction reflects once, validates a complete
   direct-field mapping and compiles an encoder. Required versus optional/null
   topology and the closed exact Go field shapes are checked before the
   definition escapes. The bounded raw tag is parsed in full, and malformed
   syntax or duplicate `i18n` keys fail before automatic matching; ordinary
   `Bind` does not scan field metadata.
5. `View.Render` rechecks message contract identity, finds one whole template by
   walking the validated locale graph, and evaluates grammar using the actual
   template locale while formatting numbers/dates with the pinned formatting
   locale. Money is not converted, a calendar date is not zone-shifted and an
   instant uses the explicit zone.
6. Plain text remains unescaped. Rich output becomes only allowlisted
   text/value/markup/bidi `Part` values. Each part retains its structural kind,
   MF2 type, direction and optional opaque `u:id`; exact numbers and dates also
   expose copied flat Intl subparts whose text joins to the parent. Top-level
   and nested nodes share one output-parts budget. Untrusted directional controls
   are refused and interpolation is FSI/PDI-isolated unless the view explicitly
   selects no isolation.
7. Direct exact number/money/percent/unit and date/time methods share that same
   boundary. Typed number options include rounding mode/priority, the closed
   ECMA-402 increment set and trailing-zero behavior; impossible option
   combinations fail during preflight. Fraction and significant digit bounds may
   be declared together or independently, leaving an omitted side to Intl's
   locale/style/currency-dependent default. Relative time accepts whole `int64`
   units through the ECMAScript-safe boundary; only after that guard does the
   pinned backend convert them exactly to `float64`. Fractional and out-of-range
   values are unsupported.
8. The standalone immutable `Formatter`, or one derived from a view, validates
   named scalar/plural/range/list/relative/duration/display-name policies once.
   Numeric and date/time ranges retain start/shared/end part provenance, and
   plural ranges use locale data rather than an endpoint heuristic. Every named
   lookup remains bounded and has an explicit not-found result. Aggregate named
   material is rejected before allocation/backend validation, retained strings
   are cloned, and every successful, failed or canceled named call emits exactly
   one terminal observation.
9. `Rendered` reports actual template locale/layer, resolved provenance,
   revision/digest, outcome and a deterministic SHA-256 `RenderKey` over every
   output-affecting input. `Explain` exposes bounded preference and fallback
   steps without text or arguments. Direct list/relative/duration/display-name
   formatters use the same immutable view and safe-parts boundary.

## A structured error keeps its machine contract

`i18n/errorsource.go`, `errs/spi.go`, `port/violations.go`,
`port/porthttp/render.go`, `crud/rpc/crudgrpc/status.go`:

1. `Snapshot.ErrorMessages` validates one complete ladder-key → message-key
   map, allowlisted violation-param conversions and argument-free field labels.
   A money conversion carries a declared currency; no unmapped parameter is
   exposed.
2. When an operation already owns a view, `Snapshot.ErrorPlan` compiles those
   mappings once and `View.ErrorMessages` binds them to exactly that snapshot,
   resolution, formatting locale, zone and presentation. The legacy requested
   locale argument cannot renegotiate a second view.
3. Its immutable adapter implements `errs.MessageSource` and
   `errs.LocalizedMessageSource`. It takes the locale already carried by the
   existing `port` context, builds a snapshot view and renders wording only.
4. Failure to map, bind or format declines the source. `port.Violations` then
   retains the existing safe violation message/code fallback. Kind, code,
   public path, partial marker and transport status are never translation data;
   Internal/500 redaction stays at the transport boundary.
5. The adapter reports the template locale that really won. `porthttp` derives
   `Content-Language` from those proven locales and CRUD gRPC emits
   `LocalizedMessage.Locale`; a legacy source causes neither projection to
   claim the requested locale.
6. The three CRUD HTTP shells and CRUD gRPC preserve a non-empty locale already
   bound in context. Their fallback header/metadata helpers select only a first
   tag. An application needing the full resolver runs it before the error
   boundary and binds its supported result through the existing locale seam.

## Overlays, publication and delayed use

`i18n/overlay.go`, `i18n/controller.go`:

1. `Overlay` clones a snapshot and replaces complete approved templates only
   where the descriptor permits the requested application or tenant layer.
   Layers strictly increase. Contract/source/review identity and locale graph
   are rechecked; no tenant identity is stored in the snapshot.
2. `NewController` takes one complete initial snapshot and finite retention,
   pin, lifetime and byte ceilings. It creates no lifecycle of its own.
3. `Current` returns the active snapshot with an opaque activation identity.
   `Activate` and `Rollback` compare that exact `Head`; stale publishers and
   revision/content collisions cannot swap state. The mutation publishes one
   pointer after all checks, leaving in-flight views unchanged.
4. Retained snapshots are pruned deterministically unless a finite `Lease`
   protects them. A lease may retrieve its snapshot until release/expiry but is
   not authentication, tenant scope or durable persistence.
5. The application owns artifact storage, polling, readiness, durable message
   intent, current-versus-pinned policy and recipient authorization. A process
   restart loses controller pins unless the application separately preserves
   the required artifact and policy.

## Offline command path

`i18n/merge.go`, `i18n/cmd/vv-i18n/main.go`, `usage.go`, `extract.go`,
`extract_analysis.go`, `merge.go`, `review.go`, `atomic.go`:

- `check` decodes canonical source, optionally reads an extracted usage
  manifest, writes a bounded deterministic report and exits nonzero for errors.
- `review` selects locale/key/scope/state and stamps contract, source and review
  identities. Approval refuses structurally invalid source.
- `merge` combines the next authoritative canonical source with the prior
  reviewed source. It preserves non-conflicting work, refuses implicit loss and
  requires explicit obsolete pruning.
- `pseudo` writes accent or RTL translations without rewriting MF2 structure.
- `compile`, `generate-go` and `export-ts` call the library contracts. Go output
  pins every `Definition`; TypeScript output contains public types only and
  explicitly sets formatting parity false.
- Every command accepts one strict standalone `frostgrove.i18n.limits/v1`
  policy. Its catalogue ceiling is passed to source decode, snapshot
  construction, merge and compile; separate source and compiled artifact
  ceilings bound their matching inputs and outputs. A fourth per-file output
  ceiling bounds each report, generated Go file, extracted usage manifest,
  TypeScript file, public manifest and publication generation file. Invalid
  policy never falls back to defaults, and source metadata cannot widen the
  operator ceiling beneath which it was decoded.
- `extract` uses the local Go tool's effective `-deps -compiled -export -json`
  graph and exact export importer, then analyzes selected local packages without
  running application initialization. It follows root-local wrappers and typed
  callbacks and recognizes static or bounded-domain calls through the complete
  definition surface. Persisted GOENV/GOFLAGS, workspaces, nested modules,
  replacements, vendor mode and overlays participate in selection and
  provenance. A private build cache, local toolchain, disabled proxy/checksum
  database and read-only module/workspace shadows prevent implicit download or
  checkout writes. Unbounded keys and escaped constructor callables are refused;
  package/dependency/type/load errors, cgo, partial roots, conflicting export
  evidence and reachable external dependencies that may hide i18n calls force
  `complete:false`. Effective-build exclusions remain in the hashed ledger but
  do not by themselves invalidate that build's proof.
- Usage v4 binds sorted unique aggregates and bidirectional source occurrences
  to canonical root/file/metadata/environment/package-graph ledgers through
  recomputable source and manifest SHA-256 digests. The decoder and `Check`
  validate counts, containment, coordinates, selected-file backing, reverse
  evidence and canonical ordering under bounded JSON work. V1-v3 decode only as
  incomplete. Ledger self-consistency is neither authenticity nor freshness, so
  a trusted release that consumes non-use evidence reruns the same
  `extract -complete ... -check` command against the physical checkout; plain
  `check -usage` performs no filesystem revalidation.
- The source and usage documents are parallel inputs: `extract` produces usage
  for `check -usage`; it is not input to `review`. The authoring branch is next
  source + previous reviewed source → `merge` → `review` → `check` →
  `compile`.
- Every output file is staged, synced and atomically renamed; a multi-file
  direct export uses best-effort rollback rather than claiming a reader-side
  atomic generation switch. `export-ts -publication-root` instead syncs an
  immutable content-addressed generation and commits one `current.json` pointer;
  readers pin that pointer, verify every file digest and recompute the public
  export address from the exact manifest and TypeScript bodies. The exported
  address helper lets an independent Go reader use the same derivation. Both
  publisher and readers walk from the filesystem or volume root through stable
  descriptors, refuse links and identity changes between inspection and open,
  and cannot be redirected by a later ancestor replacement. The publisher
  creates a missing final root and syncs its parent through the pinned parent
  descriptor. Readers bound directory enumeration; the command reader polls
  cancellation between bounded file chunks.
  `-check` compares or
  verifies desired bytes without modifying the destination.

## Where the decisions bite

- The nested `i18n/go.mod` is the one optional Unicode/MessageFormat dependency
  decision. No root or transport package imports it ([[D-033]], [[D-129]]).
- There is one runtime package and one grammar profile. No `i18nhttp`,
  `i18ngrpc`, `i18ntenancy`, `i18notel` or grammar auto-detection exists.
- Resolution, grammar locale, formatting locale, time zone and presentation
  remain distinct inputs. Snapshot/pin identity grants no authority.
- Observer output is closed operation/outcome/reason, duration and count. It
  contains no key, locale input, text, args, path, cause, revision or identity;
  observer panic cannot alter the operation.
- Every library-owned input, intermediate expansion, output and retained
  snapshot is finite. Application callbacks and external systems own their own
  budgets.

## Files

| File | Role |
|---|---|
| `i18n/catalog.go` | declaration, validation, profile and immutable snapshot |
| `i18n/source.go` | strict canonical authoring document |
| `i18n/merge.go` | loss-aware preservation of reviewed authoring work |
| `i18n/check.go` | authoring findings, coverage and usage policy |
| `i18n/artifact.go` | compiled format, compiler and strict loader |
| `i18n/locale.go` | choices, canonical matching, sealed resolution and fallback graph |
| `i18n/value.go` · `i18n/optional.go` · `i18n/struct_definition.go` | typed arguments, deferred messages, generated and compile-once reflective definitions |
| `i18n/numeric.go` · `i18n/datetime.go` · `i18n/formatlocale.go` | exact values and culturally separate formatting |
| `i18n/work.go` · `i18n/bounded_text.go` | conservative expansion and output accounting |
| `i18n/render.go` · `i18n/formatter.go` · `i18n/formatters.go` | views, rendering, safe parts, explain, named policies, scalar and range formatters |
| `i18n/errorsource.go` | immutable `errs.MessageSource` adapter |
| `i18n/overlay.go` · `i18n/controller.go` | higher layers and bounded atomic publication |
| `i18n/pseudo.go` · `i18n/generate.go` · `i18n/export.go` | offline transforms and generated contracts |
| `i18n/observer.go` · `i18n/problem.go` | privacy-safe terminal observations and bounded refusals |
| `i18n/cmd/vv-i18n/` | authoring CLI, extraction, per-file writes and generational publication |
| `port/violations.go` · `port/porthttp/render.go` | root wording seam and HTTP locale projection |
| `auth/http/authhttp/authhttp.go` · `auth/http/authfiber/locale.go` | auth-refusal locale precedence |
| `crud/http/crudnet/options.go` · `crud/http/crudgin/options.go` · `crud/http/crudfiber/options.go` | HTTP locale precedence and response writing |
| `crud/http/crudnet/handler.go` · `crud/http/crudgin/handler.go` · `crud/http/crudfiber/handler.go` | additive per-resource rendering and operation-context capture |
| `crud/http/crudnet/middleware.go` · `crud/http/crudgin/middleware.go` · `crud/http/crudfiber/middleware.go` | process/resource renderer composition at the error boundary |
| `crud/rpc/crudgrpc/locale.go` · `crud/rpc/crudgrpc/status.go` | gRPC locale precedence and status details |
| `crud/rpc/crudgrpc/handler.go` · `crud/rpc/crudgrpc/interceptor.go` | additive resource rendering across unary/stream boundaries |
| `test/i18nflow/` | full HTTP/gRPC, overlays, cache identity and edge-boundary integration |
| `scripts/i18n-consumer.sh` · `scripts/i18n_test.go` · `scripts/i18n_release_test.go` | optional dependency, lifecycle and standalone release gates |

## Tests that walk this flow

The nested module's `catalog_test.go`, `source_test.go`, `check_test.go`,
`artifact_test.go`, `locale_test.go`, `render_test.go`, `formatters_test.go`,
`overlay_errors_test.go`, `controller_test.go`, `generate_test.go`,
`export_test.go`, `pseudo_test.go`, `hardening_test.go`,
`foundation_fix_round_test.go`, `integrity_fix_round_test.go` and
`lifecycle_fix_round_test.go`, plus the struct-definition, direct-formatter,
range, plural and date-component suites, cover the package contract. `fuzz_test.go` and the
source fuzz target cover malformed inputs. `cmd/vv-i18n/*_test.go` covers every
command, direct output and generational publication. `test/i18nflow/*_test.go` walks integration through
HTTP, gRPC and cache boundaries. The scripts named above run dependency and
standalone-consumer gates with workspace resolution disabled.
