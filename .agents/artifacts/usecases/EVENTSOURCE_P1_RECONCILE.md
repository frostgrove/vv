# EVENTSOURCE PHASE 1 — RECONCILIATION

> **Refreshed 2026-09-07 against the redesigned specification.** The subject
> document — `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`, "[SPEC]" —
> was rewritten after the first reconciliation was produced. Its central shape
> changed: the live, mutable `View` is deleted, the codec moved from the store to
> the declaration, the transaction rule became [[D-118]]'s *join* rather than a
> refusal, and the store→kernel error channel became a declared closed vocabulary
> (`Outcome` / `Failure`). Seven of the earlier document's fifteen deltas were
> about mechanisms that no longer exist.
>
> **If you read the earlier version**, discard everything from its §2 (deltas)
> onward. In particular: the earlier D-2 (`ErrTooLarge` split), D-6 (`Codec` is
> non-generic and carried by the store), D-9 (`eventmemory.New` returns no error)
> and D-10 (`Backing` restates `SameDataSource`) are **closed** — [SPEC] now says
> what the tree says. The earlier D-3 (declaration panic vs. error sibling) had
> the wrong precedent and is re-answered in §3.7 below. The earlier ADR numbering
> (D-120…D-124) is **stale**: `docs/ai/decisions/D-120-…` was landed in the
> meantime and the next free number is **D-121**.
>
> **Kept:** §1, the inventory, which was written against the codebase rather than
> against [SPEC]. Every claim in it has been re-verified at `72e7d22`; six were
> wrong and are corrected in place, with the corrections listed together in §1.8.
>
> [ES] is `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`,
> [EXT] is `docs/roadmaps/2026-09-01-extension-architecture-roadmap.md`,
> [GAPS] is `.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md`.
>
> **What this document does not do.** It does not edit [SPEC] or [GAPS], it writes
> no code, and it does not decide the owner questions it identifies as owner
> questions — it narrows each to one recommendation plus the cost of the
> alternative.

---

## 0. The headline

The redesign moved [SPEC] **towards** the tree, not away from it, and it did so on
every axis the first reconciliation flagged. What is left is smaller and sharper.

Three findings dominate the plan.

1. **The store→kernel classification channel [SPEC] invented in round 6 already
   exists in `jobs`, twice, and one half of it carries the exact defect [SPEC]
   forbids.** `jobs.RejectPlacement` (`jobs/queue.go:799`) is a closed vocabulary
   a driver in another module selects from; `jobs/queue.go:808:normalizeSenderError`
   is the fail-safe default, and its default **is** uncertainty
   (`jobs.ErrAmbiguous`). But it reads the classification with a **bare type
   assertion**, `err.(rejectedPlacement)`, so a decorator that wraps a driver's
   refusal with `%w` loses it. §D.14's `errors.As`-not-a-type-assertion clause is
   therefore a correction to a live subsystem, not a new rule, and that is worth
   saying out loud in the ADR.

2. **Q21 is not a departure from the tree at all — the tree has the shape [SPEC]
   wants, under a third name nobody looked for.** `crud/sqlrepo/blueprint.go:74`
   spells the panicking call `Define` and the error-returning one `TryDefine`, and
   [[D-021]] names both by line. The earlier reconciliation's D-3 argued for
   `Define`/`MustDefine` because it had only found the `New…`/`Must…` half of the
   convention. `event.Define` / `event.TryDefine` gives [SPEC] the panic as the
   documented call **and** gives the negative tests a returned error, with no
   second construction path and a precedent [[D-021]] already cites.

3. **[SPEC] has not passed its own last gate.** [GAPS] round 10
   (`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md:5308`) is unresolved:
   GAP-100, GAP-101 and GAP-102 are `[high][immediate]`, and by
   `~/.claude/skills/econv/references/gaps.md` an immediate `high` blocks a gate.
   The round closes with a ten-item **Carry into the plan** list (`:5994`). This
   is a process fact, not a design one, and §5 is where the plan inherits it.

---

## 1. Inventory — what already exists and must be reused rather than reinvented

Re-verified at `72e7d22`. Line numbers are a starting point for a search, not an
address, in this document's own words as well as `docs/ai/flows/Index.md`'s.

### 1.1 The shape phase 1 copies: `jobs`, and what it actually is

| What | Where | What phase 1 takes from it |
|---|---|---|
| The subsystem lives in the root module, its driver in a satellite | `jobs/` + `jobs/jobspg/go.mod`, `jobs/jobsredis/go.mod` | the whole topology argument; `event/` + `event/eventmemory/` + `event/eventtest/` in the root, `event/eventpg/` a module in phase 2 |
| A second, in-tree, volatile implementation | `jobs/jobsmemory/backend.go:125:New(Limits, ...Option) (*Backend, error)`, `:25:DefaultLimits`, `:48:WithClock`, `:58:WithBackendID` | the injected clock and the "another value over the same backing" spelling. Its *constructor shape* is not what [SPEC] takes — see §3.4 |
| A parsed, unforgeable wire identifier | `jobs/identity.go:13:Name`, `:28:QueueName`, `:58:CodecID`, `:73:BuildID` — unexported field, `ParseName`, `Value()`, `String()`, `IsZero()`, `valid()` | the idiom [SPEC] deliberately **inverts** for the store-seam types, and the inversion needs an argument — see §2.1 |
| The identifier's legal domain | `jobs/identity.go:273:validRegistryName` / `:260:parseRegistryName` / `:295:asciiLowerAlphaNumeric` | the domain a family and a wire type name may use — see §3.5 |
| Bounds as named constants with a default and a hard ceiling, and no spelling for unbounded | `jobs/bounds.go:5-24` — `MaxNameBytes = 128`, `DefaultPayloadBytes = 64 << 10`, `MaxPayloadBytes = 1 << 20`, `DefaultDecodedBytes = 256 << 10`, `MaxDecodedBytes = 4 << 20`, `MaxPayloadDepth = 64`, `MaxSupportedRevisions = 8`, `MaxUpcastHops = MaxSupportedRevisions - 1` | every number §D.13 takes; §D.13 cites them correctly |
| Declaration that validates eagerly, with a sibling that does not panic | `jobs/definition.go:45:Define` / `:107:MustDefine`; **and the other spelling**, `crud/sqlrepo/blueprint.go:74:Define` (panics) / `:82:TryDefine` | the declaration seam — see §3.7, which is Q21's answer |
| The catalogue that refuses two declarations sharing a name | `jobs/catalog.go:25:NewCatalog` (duplicate check at `:46`), `:135:fingerprintDescriptors`, `:12:Declaration` (two unexported marker methods) | `event.Declaration`'s shape, and the additive answer to Q19 |
| A closed classification a driver in another module selects from | `jobs/queue.go:799:RejectPlacement`, `:791:rejectedPlacement`, `:808:normalizeSenderError`; the driver side is `jobs/jobspg/driver.go:321:placementError` | `event.Outcome` / `event.Failure` — the shape, and one defect not to copy (§2.3, §3.1) |
| Sentinels, and the sanitiser that stops a driver's text travelling | `jobs/errors.go`; `cache/errors.go:63:sanitizedError` / `:26:opaqueError` / `:86:safeErrorIs` / `:96:boundedErrorIs`; `tenancy/errors.go:57:Classify` | the refusal vocabulary's mechanism |
| A stdlib-only root subsystem is **not** the norm | `go list -deps` answers *only itself* for `./cache`, `./storage`, `./health`, `./runtime`; **`./jobs` names five first-party packages** — `utils`, `crud`, `crud/query`, `errs`, `port` | the weight budget `event` is spending — see §2.4 |

### 1.2 How `jobs` declares a payload type and its revisions — and what `event` must **not** reuse

`jobs` binds a payload type `P` to a **typed codec that carries the revision**:

- `jobs/codec.go:29` — `type Codec[P any] interface { ID() CodecID; Version() SchemaVersion; Encode(P, PayloadLimit) ([]byte, error); Decode([]byte, PayloadLimit) (P, error) }`.
- `jobs/json.go:27:JSON[V any](version SchemaVersion) Codec[V]`, `:31:TrustedJSON`, `:35:jsonCodecFor`.
- `jobs/upcast.go:25:Upcast[A, B any](from Codec[A], to Codec[B], fn func(A) (B, error)) Upcaster`. The
  upcaster is **erased**: `jobs/upcast.go:9:Upcaster` is non-generic with an unexported
  `upcast([]byte, PayloadLimit) ([]byte, error)` and an `upcasterMarker()`.
- `jobs/definition.go:341:normalizeUpcasters` sorts the hops, refuses a duplicate source
  revision, refuses a non-contiguous chain, and refuses a chain that does not terminate at
  the current codec's `(id, version)`.
- `jobs/definition.go:225:(*Definition[P]).decodePayload` is the read path: exact revision →
  decode; older revision → search the hop table, refuse if the revision is outside the
  retained window, then replay hops **as bytes**, re-encoding at every hop.
- `jobs/json.go:35:jsonCodecFor` charges the type graph once at codec construction
  (`newJSONTypeProfiler(trusted).charge(reflect.TypeFor[V]())`) and stores the result as
  `chargeErr`, surfaced through the unexported `validateCodec()` hook at `jobs/json.go:57`
  that `describeCodec` looks for at `Define` time.

**What `event` reuses.** The revision-window refusal, the contiguity and termination
checks, the `func(A) (B, error)` upcaster signature, the panic-recovery-at-every-callback
discipline (`jobs/upcast.go:71:upcastOwned` recovers into `ErrInvalid`; `jobs/queue.go:757`
and `:774` recover a driver's panic into `ErrAmbiguous`), and the idea that a declaration
re-checks its own inputs have not mutated.

**What `event` must deliberately not reuse — three things, each with a reason.**

1. **Bytes-at-every-hop upcasting.** `jobs/upcast.go:71:upcastOwned` decodes revision *r*,
   calls the upcaster, and **re-encodes** to *r+1* before the next hop. For a job whose hop
   table is erased and whose payload is stored between hops, that is right. For `event` it is
   *n* encode/decode round trips per stored event on the load path — the path §UC-011 pages
   precisely to keep bounded. §D.2's chain decodes once into the revision's reader type and
   carries a typed Go value forward. Keep [SPEC]'s shape.
2. **Revision carried by the codec.** `jobs.JSON[V](version)` makes the *codec instance* the
   revision, which is why `normalizeUpcasters` needs four validations. §D.2 derives the
   revision from the chain's position, which makes four of the five unnecessary rather than
   implemented (§INV-010). Keep [SPEC]'s shape; Q6 is settled by this.
3. **The `PayloadLimit` parameter on every codec method.** `jobs.Codec` takes a limit on
   `Encode` and `Decode`, so the codec applies the bound. §D.14's division of labour puts
   every bound in the kernel and `event.Codec[V]` (§D.2) has no limit parameter. That is the
   right split — a store that could apply a bound could apply a different one (§INV-018) —
   and it is a real difference from the sibling, not an oversight.

### 1.3 The transaction-binding proof `jobs.TransactionContext` gives, exactly

Two values plus the profile, `jobs/durability.go`:

```go
type TransactionBinding struct{ value [32]byte }          // :305
func TransactionBindingFromBytes(value [32]byte) (TransactionBinding, error)  // :307
func (b TransactionBinding) Bytes() [32]byte              // :314
func (TransactionBinding) MarshalJSON() ([]byte, error)   // :320 — always refuses

type TransactionContext struct{ backend BackendID; binding TransactionBinding; durability DurabilityProfile }  // :325
func NewTransactionContext(backend BackendID, binding TransactionBinding, durability DurabilityProfile) (TransactionContext, error)  // :331
func (c TransactionContext) Backend() BackendID           // :338
func (TransactionContext) MarshalJSON() ([]byte, error)   // :344 — always refuses
```

Minted at `jobs/jobspg/stager.go:70:(*Driver).stager(tx *sql.Tx)`: 32 bytes of entropy from
`jobs/jobspg/config.go:218:(*Driver).token()` (an `io.ReadFull` on the driver's `entropy`
reader under a mutex), plus `d.description.ID()` — the backend identity, which
`jobs/jobspg/config.go:229:defaultBackend` derives as
`sha256("frostgrove.jobs.postgres.v1\x00" ‖ schema ‖ namespace.Digest())`.

**What that proves and what it does not.**

- It proves *which backend*. `BackendID` is derived from the schema and namespace, never from
  the `*Driver` pointer, so a driver constructed per request over a borrowed lease answers
  the same identity. That is `event.Backing`'s requirement and the tree already has the right
  answer for the right reason (§UC-055).
- It is unforgeable in practice and unserialisable by contract.
- **It does not prove which transaction.** `stager(tx)` mints *fresh* entropy on every call,
  so two stagers over the *same* `*sql.Tx` produce **unequal** bindings, and
  `jobs/queue.go:864:validateStager` only ever compares `transaction.Backend()` against
  `queue.description.ID()` — never two bindings. §INV-028's "two calls that resolve to one
  live transaction answer `Same`" is therefore **not** a property `jobs` has, which is why
  §INV-028 holds the transaction's identity **by reference** instead of minting. [SPEC] is
  right and says so (§2.1, §D.12's minted-`[32]byte` row).

### 1.4 The error contract — what exists for conflict, not-found, unsupported, invalid, retryable, too-large

| Class | The sentinel | Where | How a transport reaches it |
|---|---|---|---|
| conflict | `crud.ErrConflict` | `crud/errors.go:32` | `port/kind.go:70:sentinelKind` → `errs.KindConflict` → `port/porthttp/errors.go:27:StatusFor` → 409 |
| retryable | `crud.ErrUnavailable` | `crud/errors.go:38` | same chain → `errs.KindRetryable` → **503** |
| not found | `crud.ErrNotFound` | `crud/errors.go:9` | → `errs.KindNotFound` → 404 |
| bad request | `crud.ErrBadRequest` | `crud/errors.go:24` | → `errs.KindBadRequest` → 400 |
| forbidden | `crud.ErrForbidden` | `crud/errors.go:30` | → `errs.KindForbidden` → 403 |
| **too large** | **no sentinel** | — | **only** via an `*errs.Fault` with `errs.KindTooLarge`, because `port/kind.go:17:KindOfWith` consults `errs.AsFault` *before* `sentinelKind`. The builder is `errs/build.go:25:TooLarge()`; the worked example is `port/porthttp/body.go:105:TooLarge`; the status is `http.StatusRequestEntityTooLarge` |
| unsupported / invalid | **no framework class** | — | `jobs.ErrUnsupported`, `jobs.ErrInvalid`, `cache.ErrInvalid` are per-subsystem and reach `errs.KindInternal` (500) |

The **wrapping convention** is [[D-015]] — "reachable with `errors.Is` against an exported
sentinel in package `crud`" — and the worked precedent for a root subsystem is
`tenancy/errors.go`: eleven sentinels, each `fmt.Errorf("tenancy: …: %w", crud.Err…)`.
`jobs` and `cache` do **not** do this; `tenancy` does, and [[D-116]] states why it may.

The **redaction** mechanism exists twice: `tenancy/errors.go:57:Classify` (keep a
deliberately chosen sentinel, collapse everything else to one class, let `context.Canceled`
and `DeadlineExceeded` through intact) and `cache/errors.go:63:sanitizedError` +
`:26:opaqueError` (`Error()` returns only the *category*'s text while `Is` still reaches the
cause, under a hop budget in `:96:boundedErrorIs`). `cache`'s is the one §INV-025 needs and
names, and nothing else in the tree has it.

`cache.ErrTooLarge` and `jobs.ErrTooLarge` exist as **per-subsystem** sentinels that carry no
framework class. They are the precedent for *having* the row and the counter-example for how
it must be wired.

### 1.5 The optional-capability walk, the conformance suite, and the decorator seam

- **The walk.** `cache/capability.go:201:CapabilityOf[T](backend Backend) (T, bool)` walks
  `cache/backend.go:40:BackendWrapper`, whose `Next() Backend` it follows, bounded at depth 64, with
  `cache/backend.go:97:repeatedBackend` as a cycle guard, a nil check on every hop, and
  `recover()` around every call into a wrapper. `crud`'s equivalent is
  `crud/executor.go:210:unwrapSource` with `:119:maxChainDepth = 64` and the named helpers
  `crud/executor.go:121:SourceOf`, `:228:BeginnerOf`, `:236:ReadSourceOf`.
  **[SPEC] deletes the need for this** (§INV-030: all eight `Store` methods are required),
  which is the strongest form of the [[D-061]] answer and is not a gap.
- **The suite.** `cache/cachetest/suite.go` is an exported conformance suite in a non-test
  package of the root module: `:22:Harness`, `:46:Factory func(*testing.T) Harness`,
  `:48:Run(t *testing.T, factory Factory)` dispatching **25** named `t.Run` sections. It
  imports `"testing"` in a non-test file and `make check-deps` is green, because `testing` is
  standard library and `check_deps` filters on `{{if not .Standard}}`. This settles Q9 by
  precedent. Its one consumer today is `cache/cachememory/conformance_test.go:15`.
- **Anti-vacuity already exists there, once.** `cache/cachetest/suite.go:94` **fails** a run
  when a backend declares `CapacityBounded` and supplies neither a `Capacity` probe nor a
  written reason in `Harness.CapacityNotProbed`. That is §UC-043's first anti-vacuity rule,
  implemented. The other three are new — and `cachetest` `t.Skip`s five sections
  (`:214`, `:1508`, `:1674`, `:1715`, `:1721`), which is the shape §UC-044 refuses.
- **The decorator.** `crud/repo.go:39:Middleware` / `:63:Chain` / `:76:Base.Next`;
  `storage/store.go:25:Middleware` / `:27:Chain`. `event`'s store is non-generic, so
  `storage`'s is the closer shape.

### 1.6 The rest of the seams [SPEC] names

| [SPEC] names | The tree's actual symbol |
|---|---|
| `port.Logger` | `port/log.go:8:Logger(ctx) *slog.Logger`. Phase 1 emits no lines (§INV-011), so `event` need not import `port` — and `jobs` imports it only for this, in `jobs/classifier.go` |
| `health.Contribution` | `health/health.go:43:Probe` (`Check(ctx) error`), `:54:Contribution`. The subsystem-side precedent is `cache/health.go` — a method, not a registration ([[D-091]], [[D-096]]) |
| `runtime.Runner` | `runtime/runner.go` — phase 1 contributes none (non-goal 3) |
| `crud.Source`, `Executor`, `Beginner`, `IsTransaction`, `SameDataSource`, `KeyOf` | `crud/executor.go:56`, `:23`, `:52`, `:38`, `:562`, `:524`. Plus the context binding `:315:BindExecutor` / `:341:WithExecutorFor` / `:370:ExecutorFor` / `:419:OwnedExecutorFor` / `:441:bindingFor` that `eventpg` joins in phase 2 |
| `crudsql.Transaction`, `TransactionFor` | `crud/adapter/crudsql/crudsql.go:195`, `:210`; the savepoint is `:226:savepoint` with `:232:Tx()` returning the **parent's** `*sql.Tx` |
| `utils.Opt[T]` | `utils/optional.go`. Nothing in phase 1 needs it — `Opt` is a persistence-patch primitive |
| an in-memory test seam | `crud/crudtest/recorder.go:38:Recorder` — a fake source that records SQL, not a conformance suite. `cachetest` is `eventtest`'s precedent; `crudtest` is only the precedent for "the test seam ships in the root module" |
| an opaque persistable cursor | `crud/cursor.go:19:EncodeCursor` — base64 RawURL over a JSON payload, surfaced as `crud.PaginatedResponse.NextCursor string` |
| a versioned key encoding | `cache/key.go:9:KeyCodec[K]` with `Version() KeyVersion`, `:23:KeyFunc` / `:30:MustKeyFunc`. §D.12 rejects the versioning for `event` and the rejection is right (§3.6) |
| a length-prefixed composition of parts | `cache/address.go:38-43:NamespaceOf` — a 4-byte big-endian length before each part. **This is `event.Compose`'s mechanism, already in the tree** |
| a type-graph walk | `jobs/json.go:1062:(*jsonTypeProfiler).discover` — visited map, `jsonTypeMaximumDepth`, `jsonTypeMaximumNodes`, `jsonTypeMaximumEdges` |
| a driver-side classification of its own failure | `jobs/jobspg/driver.go:321:placementError` → `jobs.RejectPlacement`; the kernel side is `jobs/queue.go:808:normalizeSenderError` |
| a nil-by-any-route predicate | `crud/executor.go:507:isNilValue` — six kinds, and **unexported**, so §INV-016's restatement is honest |

### 1.7 Naming and file organisation `event` must follow

- **Package names.** `event`, `event/eventmemory`, `event/eventtest` — [[D-035]]'s grid and
  [[D-058]]'s axis. `cachememory`/`cachetest` and `jobsmemory` are the exact precedent;
  `eventpg` in phase 2 matches `jobspg`. **No package named `event` exists in the tree
  today** (`go list ./... | grep -i event` is empty).
- **Files by concern, not by layer.** `jobs/` and `cache/` are flat directories of
  concern-named files. No `internal/`, no `types.go`, no `util.go`.
- **Receivers.** `crud`, `port`, `errs`, `cache`, `crudsql` use `this`; `jobs` and `tenancy`
  use a short domain name. §6's reading rule picks `this`, which is the tree's dominant
  receiver, and that is the better call for new packages.
- **A value type states what it is and refuses to leak.** `String()` returning a bracketed
  classification, `Format(fmt.State, rune)` forwarding to it, `IsZero()`, an unexported
  `valid()`, and `MarshalJSON` returning an error for anything that must not be serialised —
  uniform across `jobs/durability.go`, `jobs/identity.go`, `storage/types.go:Link.String` and
  `tenancy/`. §D.14 gives `Stream.String()` and `Authority.MarshalJSON` exactly this.
- **Docs, in the same change.** `docs/modules/en/<pkg>.md` **and** `docs/modules/ru/<pkg>.md`
  (52 files each today, filenames identical), a row in each `Index.md`, one flow in
  `docs/ai/flows/` with both index tables updated, a repository use case, the decisions, and
  `make api`.

### 1.8 Corrections to the earlier inventory

Six claims were wrong. They are corrected above; they are listed here so nobody carries a
stale one forward.

| Earlier claim | What is actually true |
|---|---|
| "`go list -deps ./jobs`, `./cache`, `./storage`, `./health`, `./runtime` each answer with **only themselves**" | True of the last four. **False of `jobs`**, which names `utils`, `crud`, `crud/query`, `errs` and `port` — it imports `port` for `port.Logger` in `jobs/classifier.go` and inherits the rest. This strengthens §2.4 rather than weakening it |
| "`jobs/jobspg/stager.go:20:(*Driver).Stager(tx *sql.Tx)`" | The signature is now `Stager(db *sql.DB, tx *sql.Tx) (*TxStager, error)` at `jobs/jobspg/stager.go:27`, with a comment naming [[D-118]] and a `StageIn(ctx, func(*TxStager) error) error` beside it at `:40`. The unexported `stager(tx)` at `:70` is what mints |
| "`cache/cachetest/suite.go` … 23 named `t.Run` sections … `Harness` (:22), `Factory` (:39), `Run` (:41)" | **25** sections; `Harness` at `:22`, `Factory` at `:46`, `Run` at `:48` |
| "`cache/key.go:29:MustKeyFunc`" | `:30` |
| "`crud/sqlrepo.Define` is the one that panics outright" | It panics, and **`TryDefine` at `:82` is the error-returning sibling** — the fact the earlier D-3 missed, and the one that answers Q21 |
| "the next free decision number is D-120" | `docs/ai/decisions/D-120-a-refresh-credential-names-the-generation-that-minted-it.md` exists. **D-121** is next. `FL-036` and `UC-032` are still right |

Two further numbers worth pinning: `jobs/json.go` (1407 lines) and `cache/codec.go` (1564
lines) share **20** identically-named package-level unexported functions, including
`jsonCodecFor`, `newJSONTypeProfiler`, `validateSafeJSONType`, `jsonUnsafeHook`,
`preflightJSONEncode`, `preflightJSONDecode`, the `scanJSON*` family and `skipJSONSpace`.
The `json_mode_v1.go` / `json_mode_v2.go` build-tag pair exists in **three** places —
`jobs/`, `cache/` and `cache/cachetest/` — not two.

---

## 2. The seven questions

### 2.1 `Stream`, `Version`, `Position`, `Key`: what the tree does, and what a store must be handed

**What the tree does for an equivalent.** Every wire identifier in `jobs` is a struct with a
single unexported field and a `Parse…` constructor that validates:
`jobs/identity.go:13:Name`, `:28:QueueName`, `:43:BindingName`, `:58:CodecID`, `:73:BuildID`,
`:177:IntentDigest`, `:194:InvocationID`, plus `jobs/disposition.go:11:FailureCode`. Each
carries `Value()`, `String()`, `IsZero()` and an unexported `valid()`. `cache` does the same
for `cache/address.go:19:Namespace` and `cache/capability.go:64:Tag`. `crud` has no identity
type at all: an identity is `KeyOf(v any) any` (`crud/executor.go:524`) — an opaque `any`
compared with `SameDataSource`, never parsed.

**Why `event` cannot follow the `jobs` idiom for these four, and the tree already discovered
why.** §INV-019's zero-diff claim requires a store in another package to **construct** every
value the contract makes it return. A `struct{ value string }` with only a validating
`Parse…` is constructible from outside — but a `Stream` is a *pair*, a `Version` is assigned
by the store on every append, and a `Position` and a `Cursor` are minted per row. Making
those four parsed types means four minting calls in `event` whose only caller is a store,
which is API with no reader in phase 1 — the thing `restrictions.md` §1 forbids. §D.14's
answer is right: `Key`, `Cursor` are defined `string`, `Version`, `Position` are defined
`uint64`, and `Stream` is a struct of two exported fields.

The tree has already been pushed the same way once, and the comment is still there:
`cache/address.go:70` exports `Namespace.Digest()` with the note *"until this existed,
`InvalidateTag` was a capability only a package-local backend could implement"*. That is
§INV-019's pressure, felt and answered in `cache`, one accessor at a time. `event` pays it up
front instead.

**What that costs, and §INV-015 is the reason it costs little.** `event.Stream{Family: …,
Key: …}` is legal for a caller to write and has nowhere to go: `Repo.Load` takes an `ID`,
`Repo.Append` takes an `At[S]`, `Read` takes a `Cursor`. The only surface accepting a
`Stream` is `Store`, held by A2. The values whose unconstructibility carries weight are
`At[S]` and `Commit`, and **neither crosses the store boundary in either direction** — the
kernel unpacks a token into `AppendRequest.Expected` and assembles a receipt itself
(§D.5, §D.15). That is the argument, and it holds.

**Naming and construction shape, concretely:**

| Value | Shape | How `eventpg` obtains one |
|---|---|---|
| `Key` | `type Key string` | `event.Key(column)` / `string(s.Key)` |
| `Version`, `Position` | `type Version uint64`, `type Position uint64` | conversion from the store's own columns |
| `Cursor` | `type Cursor string` | conversion; `eventpg` mints its own format and parses only its own |
| `Stream` | `struct{ Family string; Key Key }`, comparable with `==` | a composite literal from two columns |
| `Envelope`, `Record`, `AppendRequest`, `Limits`, `Capabilities` | plain structs, exported fields | composite literals |
| `Support`, `Outcome` | defined `uint8` + exported constants | the constants |
| `Backing`, `Authority` | opaque structs, **one exported minting constructor each** | `event.NewBacking(identity any)`, `event.NewAuthority(over Backing, transaction any)` |
| a classified `error` | — | `event.Failure(outcome, cause)` |

Two of those constructors return an error and are the whole of what an out-of-package store
must be *handed*: `NewBacking` and `NewAuthority`, plus `Failure`. Everything else is a
conversion or a literal. I walked `eventpg`'s eight methods against §D.14 and found no value
it cannot build from outside package `event` — which is the same walk [GAPS] round 10 did
(`:5421`) and reached the same answer.

**One naming note the tree makes worth raising.** `event.Stream` collides with nothing
(`auth/rpc/authgrpc/interceptor.go:43:Stream` is a function in another package), and
`event.Aggregate` beside `crud/aggregate.go:51:Aggregate` (an aggregation *option*) is
[[D-035]]'s non-collision. But **`event.Failure` (a function) sits beside
`jobs/durability.go:81:Failure` (a `uint8` enum of `FailureProcessCrash`, `FailureHostLoss`,
…)**, which §Q3 does not mention — it names `jobs.HandlerFailure` instead. Not a Go
collision; worth one line in the module page so a reader of both does not misread it.

### 2.2 The codec on the declaration: what to reuse verbatim, what not to, and why

**What `jobs`/`cache` already provide.**

| Piece | Where | Verdict for `event` |
|---|---|---|
| `Codec[P]` as a typed interface bound to the payload type | `jobs/codec.go:29` | **Reuse the idea, not the interface.** `event.Codec[V]` (§D.2) drops `ID()`, `Version()` and the `PayloadLimit` parameter, and adds `CanEncode() error` |
| `Upcast(from, to, fn)` producing an erased `Upcaster` | `jobs/upcast.go:9`, `:25` | **Do not reuse.** §D.2's `From`/`Then` chain carries a typed value forward; the erasure exists to serve byte-at-every-hop replay |
| Panic recovery around every application callback | `jobs/upcast.go:71`, `jobs/queue.go:757`, `:774`, `jobs/classifier.go:120` | **Reuse verbatim as a discipline.** §INV-017 recovers an upcaster and §UC-067 deliberately does not recover a fold — and the asymmetry is argued, which `jobs` does not have to do because it has no fold |
| The hardened JSON analyser: `jsonCodecFor`, `newJSONTypeProfiler`, `validateSafeJSONType`, `preflightJSONEncode/Decode`, the `scanJSON*` family | `jobs/json.go` (1407 lines), `cache/codec.go` (1564 lines) — 20 identically-named unexported functions | **Do not reuse; do not copy.** A third copy is the third. See §3.3 |
| `json_mode_v1.go` / `json_mode_v2.go` — a `goexperiment.jsonv2` build-tag pair setting one const | `jobs/`, `cache/`, `cache/cachetest/` | **Do not reuse.** It exists to disarm the hardened analyser under jsonv2. A codec that is `encoding/json` plus a byte cap plus a `CanEncode` walk has nothing to disarm |
| The unexported `validateCodec() error` hook a codec may implement, consulted at declaration | `jobs/json.go:57`, read by `describeCodec` | **Superseded, and by something better.** §D.2 makes `CanEncode() error` an *exported method of the interface*, so the question is asked by the declaration rather than discovered by an interface assertion. That is the [[D-061]]-shaped fix applied before the defect exists |
| Type-graph bounds on a reflective walk | `jobs/json.go:189-192` — depth 1024, nodes 1024, edges 4096 | **Reuse.** The house rule is that a reflective walk carries its own bound (`crud`'s `maxChainDepth = 64`, `cache`'s depth 64). `CanEncode`'s walk needs one |

**Why moving the codec is the right call and the tree agrees more strongly than §D.2 claims.**
`jobs` puts the codec on the **data** — `jobs.DefinitionSpec[P].Codec`, and
`jobs.Upcast(from Codec[A], to Codec[B], fn)` carries *one codec per hop*. So the
per-revision codec [SPEC] calls a design choice is what `jobs` already does; what [SPEC] adds
is deriving the revision from the chain instead of typing it. The store-owned codec round 3
had was the shape with **no** precedent in the tree at all.

**The one thing to carry forward that §D.2 does not say.** `jobs`'s charge model is
computed **once at codec construction** and cached (`chargeErr`), not at declaration. §UC-056
asks the question once per retained revision at declaration; §UC-055 forbids any per-`Bind`
walk. Both are satisfiable by the `jobs` arrangement — `event.JSON[V]()` charges when the
codec value is built, `CanEncode()` returns the stored answer, and `Declare` calls it once
per link. That makes §D.4's "`Bind` costs four constant-time checks" true by construction.

### 2.3 [[D-118]] and the transaction join: what the tree does today, and how not to be a second answer

**What `jobspg.Driver.Place` does, line by line** (`jobs/jobspg/driver.go:15-42`):

```go
if d.source != nil {
    executor, found := crud.ExecutorFor(ctx, d.source)     // crud/executor.go:370
    if found {
        if !crud.IsTransaction(executor) {                 // crud/executor.go:38
            return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrUnsupported)
        }
        tx, ok := crudsql.Transaction(executor)            // crud/adapter/crudsql/crudsql.go:195
        if !ok {
            return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrUnsupported)
        }
        stager, err := d.stager(tx)
        ... // staged inside the caller's *sql.Tx
    }
}
// not found: fall through to d.place, which opens its own transaction — autocommit
```

Three answers, keyed on what the driver finds bound **for its own `crud.Source`**: nothing →
its own transaction; a transaction → join; an executor that is not a transaction → refuse.
That is §INV-041's table verbatim. `event` must be the same answer, and §D.6 already says the
right thing about *how*.

**The one correction §D.6 makes to `jobspg`, and it is right.** §D.6 requires the **two-step**
form — `crud.ExecutorFor(ctx, source)` then `crudsql.Transaction(executor)` — rather than the
one-call `crudsql.TransactionFor(ctx, source)` (`crud/adapter/crudsql/crudsql.go:210`), because only the two-step
form distinguishes *nothing bound* from *bound but not a transaction*. `TransactionFor`
collapses both into `(nil, false)`. `jobspg` already uses the two-step form and adds
`crud.IsTransaction` in between, which `crudsql.Transaction` performs itself at `:196`. The
extra call is redundant, not wrong.

**What `event` must do differently, and it is one thing.** `jobspg` refuses with
`jobs.RejectPlacement(jobs.ErrUnsupported)` — a value in the *jobs* vocabulary. §D.14 gives
`Store.Transaction(ctx) (Authority, error)` **one** failure it may report, returned bare, and
the kernel maps it to `ErrAmbientNotTransaction`. That is strictly better: `eventpg` returns
its own sentinel and never selects an `Outcome` for it, so there is no way to spell the
refusal two ways.

**The savepoint case, answered from the code.** `crud/adapter/crudsql/crudsql.go:218:(*Tx).Begin`
issues `SAVEPOINT vv_sp_<n>` and returns a `*savepoint` (`:226`). `(*savepoint).Tx()` at
`:232` returns **`this.parent.tx`** — the parent's `*sql.Tx`. `crudsql.Transaction(executor)`
finds `Tx() *sql.Tx` and therefore returns the *parent* `*sql.Tx` for a savepoint and for its
parent alike. Under §INV-028 — an authority **is** its transaction's identity held by
reference — `NewAuthority(backing, tx)` inside a savepoint and inside its parent hold the
same pointer, so `Same` holds **by construction** and nothing has to remember it. §UC-049's
savepoint paragraph is correct against the code.

What that means for a seal: a claim made inside a savepoint is a claim about the parent's
commit, which is the honest reading, and §UC-049 says so. It also means **no phase-1 API
spells "these two writes survive together even if this savepoint rolls back"**, and §UC-049
says that too. Nothing is owed here beyond the phase-2 assertion already in §9 item 8.

**Two hazards in the tree the plan must know about.**

1. **`crudsql.Transaction` reaches through a bare type assertion**, not through a named
   bounded unwrap: `executor.(interface{ Tx() *sql.Tx })`, then
   `executor.(interface{ Unwrap() Queryer })`. `crud.IsTransaction` above it *does* walk
   `unwrapSource`, so an executor decorator that implements `SourceUnwrapper` passes the
   transaction test and then fails the extraction — `(nil, false)` — and `eventpg` would
   report `ErrAmbientNotTransaction` for a real transaction behind a decorator. This is
   [[D-061]]'s failure in `crudsql`, it is pre-existing, and it is **not** phase 1's to fix;
   it is a named risk for phase 2 and a candidate finding against `crudsql`.
2. **`crud/executor.go:441:bindingFor` has a `strict` arm** that returns
   `ExecutorScopeMismatch` when the chain holds a strict binding for a *different*
   datasource. `WithExecutorFor` sets `strict := IsTransaction(e) && SameDataSource(key, KeyOf(e))`
   (`:352`). For `crudsql`, `KeyOf(db)` is the `*sql.DB` and `KeyOf(tx)` is the `*sql.Tx`, so
   `strict` is **false** and §UC-028's "two `Within` contexts chained for two backings both
   proceed" control survives. It survives by a type mismatch rather than by design, so the
   plan should pin it with a test in phase 2 rather than assume it.

### 2.4 `Backing.Equal` versus `crud.SameDataSource`, and what the checks actually enforce

**Does `event` import `crud`?** Yes, and §D.15's import table is the enumeration: three
`crud` symbols (`SameDataSource`, `ErrConflict`, `ErrUnavailable`) plus `errs` for
`KindTooLarge`. `crud.ExecutorFor`, `crud.Source` and the rest of the executor vocabulary are
**not** named — a store resolves its own executor inside its own package, which is Q4's
answer and matches `jobspg` exactly.

**What `make check-tiers` enforces** (`scripts/checks.sh:63`), three arms and none of them
reaches `event`:

- `TIER0=(crud crud/crudtest crud/query errs errs/sqlerr port port/porthttp utils)` — each
  **listed** package may import only other listed packages. It constrains what `crud` and
  `errs` import, never who imports them.
- `TIER0_STDLIB=(crud utils)` — those two may import only the standard library and `SHARED`.
- `TIER0_SEALED=(errs)` — `errs/...`, including its tests, may import only the standard
  library and `errs/...`.

Adding `event` to none of these lists is the whole of §D.15's tier answer, and [[D-048]]'s
manifest stays closed. `event` is an ordinary root package with ordinary first-party
dependencies, which is what `jobs` is.

**What `make check-deps` enforces** (`scripts/checks.sh:40`): it runs
`go list -deps -test -tags=integration -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...`
over the **root module** and fails if any path does not start with
`github.com/frostgrove/vv`. It measures **third-party** weight only. `crud`, `errs` and
`utils` are first-party and invisible to it. `testing` is standard library and invisible to
it, which is why `cache/cachetest` compiles under it and why `eventtest` will.

**What `make check-utils` enforces** (`:116`): nothing under `utils/` may import a package
matching `SUBSYSTEMS=(crud auth port remote storage app tenancy)`. Adding `event` to that
list makes `utils/` unable to import it — which is the point of the row — and has no other
effect, since `SUBSYSTEMS` is read by `check_utils` and by nothing else.

**The measured weight.** `go list -deps ./event/...` will name `utils`, `crud`, `errs` and
the three `event` packages: **three** first-party packages outside itself. `./jobs` names
**five** (`utils`, `crud`, `crud/query`, `errs`, `port`) — for `port.Logger`. `./tenancy`
names two. So `event` is between `tenancy` and `jobs`, and the subsystem it is modelled on
already pays more for less.

**Where `Backing.Equal` must not become a second answer, and where it deliberately does.**
`crud/executor.go:562:SameDataSource` is four steps: `nil → false`, type mismatch `→ false`,
non-comparable `→ false`, then `a == b`. §INV-016 makes `Backing.Equal` **be** that call, and
that is right for the reason §INV-016 gives: it is a comparison performed on every append,
and two answers diverging would change which programs are admitted (§UC-031, §UC-054).

The one part it cannot do is the typed nil. `SameDataSource(typedNil, typedNil)` answers
**true**: a typed nil in an `any` is not `== nil`, the types match, the value is comparable,
and `a == b` holds. So two stores that both forgot to set an identity would compare `Equal`.
§INV-016 closes that at the **constructor** instead — `NewBacking` and `NewAuthority` refuse
an identity that is nil by any of six routes — and restates `crud/executor.go:507:isNilValue`
over `reflect` because that function is **unexported** (verified). The duplication is
argued correctly: a constructor-time total function whose two answers, if they ever diverged,
both refuse, is a different risk from a per-append comparison whose two answers decide
admission. The six-case control §INV-016 asks for is what keeps it honest.

**One thing the plan owes that neither document states.** `crud.SameDataSource` is in
`TIER0_STDLIB`, so it cannot grow a dependency, but nothing pins its four steps against
`event`'s use of them. §INV-016's fourth falsification — driving a pair of `crud.Source`
values through **both** `crud.SameDataSource` and `Backing.Equal` and asserting the answers
are identical — is the test that catches a future change to either. Keep it; it is cheap and
it is the only thing standing between two answers.

### 2.5 The conformance suite: the precedent, and what `check-triplets` requires

**`cache/cachetest` is the precedent and `crud/crudtest` is not.** `cachetest` is an exported
suite: a `Harness` the implementation fills, a `Factory func(*testing.T) Harness`, and a
`Run(t, factory)` that dispatches 25 named `t.Run` sections. `crudtest` is a recording fake
source, used by tests all over the tree, and has no suite shape at all. §D.9's `Factory` with
four hooks (`New`, `Begin`, `Sibling`, `Fail`) is `cachetest.Harness` with the affordances
moved from fields to functions, which is the right move for the reason §UC-043 gives:
construction can fail and only the factory knows how to report it.

Three things `eventtest` takes from `cachetest` and one it must not:

- **Take** the `*testing.T` decision (Q9), settled by `cachetest` importing `"testing"` in a
  non-test file with `check-deps` green.
- **Take** the anti-vacuity mechanism that already exists: `cache/cachetest/suite.go:94`
  fails when a backend declares a capability and supplies neither a probe nor a written
  reason. That is §UC-043's rule 1, and the field it uses — `Harness.CapacityNotProbed` — is
  the "not certified" vocabulary §UC-044 asks for, already in service.
- **Take** the section-name-as-vocabulary property; `cachetest`'s section names are already
  the failure's words.
- **Do not take** `t.Skip` for a claimed capability. `cachetest` skips at `:214`, `:1508`,
  `:1674`, `:1715` and `:1721`; §UC-043's rules 1 and 2 forbid the first shape and require a
  run in which everything claimed was skipped to fail.

**Self-falsification has no precedent anywhere in the tree.** Nothing in `cachetest`,
`crudtest` or `test/integration` runs the suite against a deliberately defective
implementation. §UC-045's six defects are genuinely new work and the plan should price them
as such. The nearest thing is the **control-case** discipline, which does exist and is
mandated by `CLAUDE.md`: `test/integration/gate_relscope_test.go:81`'s `"not declared"`
subtest asserts the leak *is* there without the declaration, so the positive test cannot pass
vacuously. Every §D.9 section already carries a control in the same spirit.

**What `make check-triplets` requires** (`scripts/checks.sh:150`). It reads a hard-coded
list at `:19`:

```
TRIPLETS=(
  'crud/http/crudnet,crud/http/crudgin,crud/http/crudfiber'
  'auth/http/authnet,auth/http/authgin,auth/http/authfiber'
  'auth/access/http/accessnet,auth/access/http/accessgin,auth/access/http/accessfiber'
)
```

For each set it extracts every `^func Test…` name from `*_test.go`, subtracts the names
declared in `routing_test.go` and `binding_test.go`, and fails if the remaining sets differ.
**It says nothing about `event`.** `eventmemory` and a future `eventpg` are not a triplet —
they are two implementations of one contract, and what holds them equal is `eventtest.Run`
itself, run from each implementation's own package. That is a stronger tie than matching test
names, because it compares behaviour rather than identifiers, and `cachememory` already
demonstrates the shape.

The plan should **not** add an `event` row to `TRIPLETS`. It should note, once, why: the
triplet rule exists because three HTTP bindings have no shared executable contract, and
`event` does.

### 2.6 The executable zero-diff check

**What the existing checks look like.** `scripts/checks.sh` is a bash file of nine functions
dispatched by a `case` at `:322`, each printing `check-<name>: ok` and returning non-zero
with a plain-words explanation otherwise. They fall into three shapes:

- **Graph shapes** — `check_deps` and `check_tiers` shell out to `go list -deps` and grep the
  answer (`:40`, `:63`).
- **File shapes** — `check_todo` finds `TODO.md` beside `*.go` (`:187`); `check_replaces`
  parses `replace` directives with the `awk` helper in `scripts/common.sh` (`:200`);
  `check_workspace` compares `go.work`'s `use` block against modules discovered by
  `find . -name go.mod` (`:305`).
- **Regeneration shapes** — `check_otel_schema` re-runs a generator with `-check` and fails
  on a diff (`:295`).

`make check` runs eight of them (`:323`); `check_otel_module` and `check-otel-consumer` are
separate targets. `scripts/checks_test.go` tests several of them against temporary fixtures,
which is the pattern a new arm should follow.

**Why §INV-019's own spelling cannot run.** `git tag` is **empty** — the repository has 168
commits and no tag — so `git diff --stat <phase-1-tag> -- event/ ':!event/eventpg'` has no
left-hand side. `docs/api/surface.md`'s own header says "at the first tag", and
`scripts/release.sh:34` is where tags are created. Waiting for a tag means the check does not
exist for the whole of phase 1 and phase 2, which is exactly when it is load-bearing.

**Three shapes that do run today, in order of what they buy.**

1. **A recorded surface baseline for `event/`, checked by regeneration.** `scripts/modules.sh:134:api`
   already produces `docs/api/surface.md` (2739 lines) by running `go doc -short` over every
   package of every module. A `check_surface` arm that regenerates into a temp file and diffs
   the **`event/…` sections** against the committed baseline fails on exactly the change
   §INV-019 forbids: a new exported symbol, a changed signature, a removed one. It is the
   `check_otel_schema` shape, it needs no tag, and it makes §9 item 9 — the exported-surface
   baseline [ES]'s tenth obligation asks for — an arm of `make check` rather than a habit.
   **Correction to [SPEC] §9 item 9:** it says "this document has method inventories and no
   baseline". The baseline exists; what is missing is a check that reads it.
2. **A git shape that works without a tag.** `git diff --stat $(git merge-base HEAD origin/main) -- event/ ':!event/eventpg'`
   answers the same question relative to a branch point rather than a tag, and degrades to a
   skip with a printed reason when there is no `origin/main`. It is weaker — it goes green
   when phase 2 lands on `main` — so it is the second half, not the first.
3. **The compile-time half, which exists on day one and needs no script.** §INV-019's own
   second mechanism: `eventtest`'s test package builds a trivial second store — an
   append-only slice returning a classified failure of every `Outcome` — and runs `Run`
   against it. If any value the contract requires needed `event`-internal access, that
   fixture would not compile. This is the strongest of the three and the only one that
   catches the defect at the moment it is introduced.

**Recommendation:** ship (3) in phase 1 as part of `eventtest`, ship (1) as
`scripts/checks.sh:check_surface` plus a `make check-surface` target and a row in
`make check`, and record (2) in the ADR as what replaces it after the first tag. Do **not**
write a check that silently passes because it cannot find its comparison point — that is the
`GOWORK=off` govulncheck failure `CLAUDE.md` already names, in a new place.

### 2.7 The ADR agenda

Four decisions, `D-121`…`D-124`, written out in §7. The next free number is **D-121**
(`docs/ai/decisions/` holds `D-001`…`D-120`, and `D-120` is the refresh-credential decision
landed since the earlier reconciliation). `docs/ai/decisions/Index.md` carries the
`in force from phase N` status and the `Proven by (owed)` convention (`Index.md:31-40`),
which is exactly what a decision landing with phase 1 needs.

---

## 3. Deltas

Ten. **§3.1–§3.4** move a public contract or an import; **§3.5–§3.7** are shape; **§3.8–§3.10**
are scope, cost and process. Numbering does not continue the earlier document's, because the
earlier document's deltas were about a design that no longer exists.

### 3.1 The classification channel exists in `jobs`, and its kernel side has the defect §D.14 forbids

- **[SPEC]** §D.14: a store selects an `Outcome`; the kernel maps it; the kernel finds it with
  **`errors.As`** so a forwarding decorator that wraps keeps its classification. §INV-045.
- **The tree.** `jobs/queue.go:799:RejectPlacement(reason error) error` accepts one of six
  `jobs` sentinels and returns `rejectedPlacement{reason}` (`:791`), whose `Is` compares
  `target == e.reason`. The driver side is `jobs/jobspg/driver.go:321:placementError`. The
  kernel side is `jobs/queue.go:808:normalizeSenderError`:

  ```go
  func normalizeSenderError(err error) error {
      if errors.Is(err, ErrAmbiguous) { return ErrAmbiguous }
      if rejected, ok := err.(rejectedPlacement); ok { return rejected.reason }
      return ErrAmbiguous
  }
  ```

  The default **is** uncertainty, which is §UC-060's fail-safe default independently
  re-derived. `jobs/queue.go:757:place` and `:774:stage` also recover a driver panic into
  `ErrAmbiguous` and treat "an error with a non-zero result" as ambiguous.
- **Resolution — [SPEC] wins, and the delta is a finding against `jobs`.** The type assertion
  `err.(rejectedPlacement)` is not `errors.As`. A decorator wrapping a driver's
  `RejectPlacement(ErrConflict)` with `%w` — the ordinary observing decorator, blessed for
  `event` by §UC-048 — turns a conflict into `ErrAmbiguous`: one class converted into another
  **by the kernel**. `event` must use `errors.As`, §D.14 says so, and the ADR should name
  `jobs`'s version as the thing the rule was learned from. Whether `jobs` is fixed is a
  separate change with its own review; it is **not** phase 1's, and it should be raised as a
  finding rather than folded in.
- **Second half, and it is [SPEC]'s advantage.** `jobs` reuses its *sentinels* as the closed
  classification vocabulary, so `RejectPlacement`'s `switch` on `reason` is a list of six
  pointers and a wrong one degrades to `ErrInvalid`. §D.14's `Outcome` is a defined `uint8`
  with six named constants, so the vocabulary and the sentinels are two things and the map
  between them is a table. That is the better shape and it should be said in the ADR, because
  a reader who finds `RejectPlacement` first will otherwise read `Outcome` as a second answer
  to one question.

### 3.2 `Support` is a tri-state, and both existing capability structs are plain `bool`

- **[SPEC]** §D.14: `type Support uint8` with `Unstated` as the zero value; §INV-043 refuses a
  store answering `Unstated` at `Bind` **and** at `Read`.
- **The tree.** `jobs/durability.go:223:Capabilities` is five `bool`s;
  `storage/types.go:211:Capabilities` is six `bool`s. Neither can tell *unclaimed* from
  *not stated*. `jobs/durability.go:218` carries a comment recording that two capabilities
  were **deleted** because "a capability nothing can honour is worse than an absent one" —
  the same family of defect, found the hard way, in the sibling.
- **Resolution — [SPEC] wins, and it is a new pattern that needs recording.** The argument in
  §INV-043 is sound and the tree has no counter-argument, only an absence. But a tri-state
  capability is the first in the repository, so the module page and the ADR must say what it
  is for, or the next capability struct someone writes will be `bool`s again and the two
  shapes will sit side by side with no reason visible.
- **What it costs:** every capability field must be set explicitly at construction, in
  `eventmemory`, in `eventpg`, and in all three `eventtest` fixtures. That is the point.

### 3.3 `event.JSON` must not be a third copy of the hardened analyser, and the copy count is now three for one file

- **[SPEC]** §D.2: `event.JSON[V]() Codec[V]` with `Encode`, `Decode`, `CanEncode`.
- **The tree.** `jobs/json.go` (1407 lines) and `cache/codec.go` (1564 lines) share **20**
  identically-named package-level unexported functions. The `json_mode_v1.go` /
  `json_mode_v2.go` build-tag pair exists in **three** directories — `jobs/`, `cache/` and
  `cache/cachetest/`.
- **Resolution — a third shape.** `event`'s codec is `encoding/json` plus: (a) the byte cap
  checked before decode and after encode, which the kernel owns anyway (§D.14's division of
  labour); (b) `CanEncode` refusing the type classes that cannot round-trip deterministically,
  over a **bounded, visited-set** walk on `jobs/json.go:1062:discover`'s model with its three
  limits; (c) nothing else. No scanner, no transient-work charge model, no build-tag pair.
  The threat-model argument is real: a job payload and a cache value arrive from a queue or a
  shared server and are adversarial; an event payload arrives from this system's own
  append-only log behind a byte cap.
- **What the ADR records:** the third copy was refused, the second copy is pre-existing debt,
  and the trigger for extracting a shared stdlib-only analyser is a fourth consumer or the
  first defect fixed in one copy and not the other. Without that paragraph, an ECONV DRY
  audit of `event` will read the smaller codec as the third instance and the conversation
  will be had a third time.

### 3.4 `eventmemory`'s constructor pair, and why the tree's memory-store shape does not fit

- **[SPEC]** §D.4: `NewLog(LogSpec) (*Log, error)` and `New(Spec) (*Store, error)`, with the
  two **data-bearing** numbers (`MaxPayload`, `MaxKey`) on the `Log` and the operational ones
  on the `Store`.
- **The tree.** Both volatile implementations use `New(Limits, ...Option) (*Backend, error)`:
  `jobs/jobsmemory/backend.go:125` and `cache/cachememory/backend.go:122`. Drivers use a
  config struct: `jobspg`, `jobsredis`, `storage.New(*Config)`.
- **Resolution — [SPEC] wins, and its reason is better than "match the drivers".** Splitting
  the log from the store makes §UC-054's agreement requirement **structural**: two store
  values over one `*Log` cannot disagree about `MaxPayload` or `MaxKey` because neither owns
  them. No amount of `...Option` gets that. The earlier reconciliation's D-9 — "`New` must
  return an error" — is closed: §D.4 returns one, and §UC-006 states the rule once.
- **What survives from the memory-store precedent:** the injected clock
  (`jobsmemory.WithClock`, `cachememory.WithClock`), and `jobsmemory.WithBackendID`'s idea
  that the identity is a settable value rather than the pointer — which for `eventmemory` is
  the `*Log` itself.

### 3.5 The declared-identifier domain: one kernel text rule is looser than everything the tree does

- **[SPEC]** §2.1 and §INV-033: **one** rule for keys and declared identifiers alike —
  non-empty, valid UTF-8, no NUL, no control character, within cap.
- **The tree, three data points.** `jobs/identity.go:273:validRegistryName` — must begin and
  end `[a-z0-9]`, may contain `.`, `-`, `_` as **non-repeating** separators, ASCII only,
  capped at `jobs.MaxNameBytes = 128`. It governs a definition name, a queue name, a binding
  name, a codec ID and a failure code. `cache/address.go:81:validNamespacePart` — non-empty,
  no surrounding whitespace, every rune in `0x21..0x7e`, capped at 128.
  `crud`'s identifiers are validated by `crud.TableRef` and the schema reflection, not by a
  text rule.
- **Resolution — split the rule, and only the half [SPEC]'s argument does not cover.**
  §INV-033's argument is about the **key**: a per-store key byte domain was a dial nobody
  could calibrate, and one kernel-wide rule replaces it. That argument stands and the key
  half should stay as written. It says nothing about the **declared identifier**, which is a
  different object: a family and a wire type name are chosen by a programmer, appear in every
  store's identifier space, and every example [SPEC] uses (`accounts.account`,
  `accounts.credited`, `orders.order`) is legal under `validRegistryName`. Take
  `validRegistryName`'s domain for the declared-identifier half, at `MaxNameBytes = 128`,
  re-implemented in `event` (18 lines).
- **What it costs:** the "one rule" property in §2.1's text-rule row becomes "one rule for
  keys, a narrower one for declared identifiers", which is one more sentence and one more
  conformance case (`stream identity` gains a declared-identifier row). What it buys is that
  a family with an emoji in it — legal under §INV-033 as written — cannot be a PostgreSQL
  identifier, a topic name, a file name or a URL path segment, and phase 2 discovers that at
  the first `CREATE TABLE` rather than at declaration.
- **Owner's call**, and it is narrow. If the owner keeps the single rule, the cost is stated
  and §UC-004's trigger list is unchanged; the risk lands in phase 2.

### 3.6 `Compose`'s mechanism already exists, and the versioned-key rejection is right

- **[SPEC]** §D.1: `Compose(parts ...string) Key` length-prefixes each part so no two distinct
  part lists render to one key. §D.12 rejects a versioned key rendering on `cache.KeyCodec`'s
  model.
- **The tree.** `cache/address.go:38-43:NamespaceOf` writes a 4-byte big-endian length before
  each part into the hash. That is `Compose`'s mechanism, verbatim, already in the repository
  and already load-bearing for cache-key injectivity.
- **Resolution — take it, and cite it.** §D.12's rejection of the versioned rendering is also
  right for the reason it gives: a cache key may miss, an event stream may not, and bumping a
  key version orphans every existing stream. Record the rejection in the ADR so the next
  reader who finds `cache/key.go:9:KeyCodec`'s `Version()` does not "fix" it.
- **One thing [SPEC] should take from `cache` and has not:** `cache`'s key encoder returns
  `([]byte, error)`; §D.1's mapper is infallible `func(ID) Key`. An identity that cannot be
  rendered has to render *something*, and §UC-051 catches it one door later with a refusal
  that names the family and not the reason. A fallible mapper is one `error` per mapper and
  turns a post-hoc validation into the mapper's own refusal. **Recommend: fallible mapper**,
  with the key validation kept as the backstop. This is the same recommendation the earlier
  reconciliation made and [SPEC] has not taken it; it is worth raising once more and then
  dropping.

### 3.7 Q21 is answered by a spelling nobody looked for: `Define` / `TryDefine`

- **[SPEC]** §UC-004 and Q21: the declaration panics, there is no error-returning sibling, and
  the tree "does the opposite in eight places".
- **The tree, and the third shape.** Two conventions were found before:
  `Define`/`MustDefine` (`jobs/definition.go:45`/`:107`, `app/module/module.go:78`/`:120`)
  and `New…`/`Must…` (`jobs/catalog.go:25`/`:71`, `cache/declaration.go:156:NewSet`/`:181:MustSet`,
  `cache/key.go:23:KeyFunc`/`:30:MustKeyFunc`, `crud/executor.go:280:NewSession`/`:300:MustSession`,
  `port/pathmap.go:43:NewPathMap`/`:99:MustPathMap`). The third is
  `crud/sqlrepo/blueprint.go:74:Define` — which **panics** — beside `:82:TryDefine`, which
  returns `(*Blueprint[M, ID, U], error)`. `DefineInSchema`/`TryDefineInSchema` is the same
  pair at `:90`/`:98`.
- **[[D-021]] already names it.** Its *Where it lives* section reads
  `crud/sqlrepo/blueprint.go:Define` — panics; `TryDefine` is the same without it, and its
  *Proven by* names `TestBadDeclarationsPanicEarly` in `crud/sqlrepo/repository_test.go` for
  the panic and `crud/sqlrepo/blueprint_edge_test.go` — "`TryDefine`, the same checks without
  the panic" — for the refusals.
- **Resolution — both win, and there is nothing to trade.** Ship `event.Define` /
  `event.Declare` **panicking**, which is what §UC-004 wants and what the DX sketch already
  writes, and `event.TryDefine` / `event.TryDeclare` returning `(…, error)`, three lines
  under each. §INV-013's "no second, unsealed way to build a table" holds because the two
  calls return the *same* value through the same code path. §UC-004's negative tests —
  nine triggers, the largest cluster of negative cases in the phase — are written against the
  returned error rather than nine `recover()` blocks, which is `crud/sqlrepo`'s own split.
- **What [SPEC] must change:** §UC-004's "**There is no error-returning sibling of the
  declaration calls**" and §D.12's row *An error-returning sibling of the declaration calls*.
  Both are stated against the `Define`/`MustDefine` inversion, which is not what is being
  proposed. Q21 closes.

### 3.8 `eventmemory` and transactions: the tree still says no twice, and [SPEC] still says yes

- **[SPEC]** §UC-040, §UC-028…§UC-033, §D.9's `transactions` section, Q12.
- **The tree, re-verified.** `jobs/jobsmemory/backend.go:120-122` asserts `jobs.Sender`,
  `jobs.DeliveryDriver` and `jobs.Controller` — **not** `jobs.Stager`.
  `cache/cachememory/backend.go:79-82` asserts `cache.Backend`, `BatchReader`,
  `BackendDescriber` and `HealthChecker` — **not** `cache.Transactional`.
  `cache/cachetest/suite.go` has **no** transaction section. The tree's answer to the
  identical question, asked twice, was no both times.
- **Resolution — [SPEC] wins, narrowly, and the plan must price it.** §UC-040's asymmetry is
  the argument and it is correct: `jobs.Stager` is an *optional producer path* — a deployment
  that never stages still exercises `Enqueue` end to end in memory — while `Within` is on the
  main write path of any application that writes anything else in the same transaction, which
  is the composition [ES] names. Refusing it in memory leaves group E — ten use cases, five
  sentinels and a whole conformance section — unexercised by anything in the root module, and
  `eventpg` would be the first code ever to run them.
- **The cost, stated so the plan carries it:** staged writes, re-validation of the expected
  version at commit, a second concurrency surface under `-race`, rollback discarding staged
  positions with the gap burned (§UC-029, §INV-009), and one of the three `eventtest`
  fixtures (the leaky-rollback store, §UC-045) needing transactions of its own. Realistically
  30–40% of `eventmemory`.
- **The cheaper fallback, unchanged:** `eventmemory` declares `Transactions: Unsupported`, the
  `transactions` section reports not certified, and phase 1 ships a `## Debt` item naming the
  ten use cases with no test. That is honest and it is what `jobs` did.

### 3.9 `SUBSYSTEMS` and a per-extension graph test

- **[SPEC]** Q11: open, with a recommendation.
- **The tree.** `scripts/checks.sh:18` — `SUBSYSTEMS=(crud auth port remote storage app tenancy)`.
  `jobs`, `cache`, `health`, `runtime` and `otel` are **not** in it, so `check_utils` would
  not notice a package under `utils/` importing `jobs`. It is read by `check_utils` and by
  nothing else. The real optionality check is `scripts/tenancy_test.go`, three tests written
  for one extension by a package-path constant (`const extension = "github.com/frostgrove/vv/tenancy"`).
- **Resolution — do both, and say what is not being fixed.** Add `event` to `SUBSYSTEMS`
  (one row; `event` is a subsystem by [[D-058]]'s definition). Add `scripts/event_test.go` on
  `tenancy_test.go`'s model with the three tests it carries:
  - `TestNoBaseSubsystemDependsOnTheOptionalExtension` — walks `go list ./...` edges, refuses
    any non-`event` package importing `event/…`, and fails outright if fewer than 50 packages
    were listed, so the test cannot prove nothing.
  - The cost test — `tenancy_test.go`'s second test computes the core's first-party graph
    against `./crud`'s and allows exactly that. **The `event` version must allow `crud`
    *and* `errs` explicitly**, or it fails on its first run and gets loosened under pressure.
    That is §6's R6.
  - `TestTheExtensionDoesNothingWhenItIsMerelyImported` — the grep for `^func init\(`,
    `^var .*= *(os\.Getenv|regexp\.MustCompile)`, `go func\(` and `go [a-zA-Z]` over every
    `GoFile` of `./event/...`. That is Q13's goroutine half, and it is copyable verbatim.
  **Do not** generalise `tenancy_test.go` into a table over extensions in this phase: the
  file's own comment argues against a list. Record the `jobs`/`cache`/`health`/`runtime`/`otel`
  gap in `SUBSYSTEMS` as a separate finding.
- **Q13's second half is still new work.** No AST check over package-level mutable state
  exists anywhere. §INV-013's table is the predicate — it targets **mutation**, not kind, so
  the twenty-two sentinels and the `*Aggregate`/`*Fact` declaration idiom pass — and
  `scripts/docs_test.go` already carries the `go/ast` + `go/parser` plumbing to build it from
  (`:526:TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` and its helpers).

### 3.10 [ES]'s E0 blocker, and the roadmap rows that go stale

- **[ES]** E0 decision 1 (`:558`): "The aggregate, and the consumer requirement that justifies
  event sourcing over CRUD plus audit — **Open. Until a named aggregate exists, E1 does not
  start.**" E0 decision 3 (`:560`): "Whether a root `event` vocabulary package is created —
  **No.** One consumer does not justify a base package; a second store would reopen it."
  `docs/roadmaps/Roadmap.md:27` carries a summary row: *"15 | Optional PostgreSQL
  event-sourcing extension | a named aggregate; the rest of E0 has an answer | no"*, and
  `:325` opens §15 with "No event-sourcing or outbox package exists today".
- **The tree.** No aggregate exists and none is proposed.
- **Resolution — the ADR answers both rather than routing around them, and E0 decision 3
  answers itself.** Its own wording — *"a second store would reopen it"* — is satisfied on
  day one: `eventmemory` is a complete implementation and `eventtest` is the concrete
  conformance obligation [[D-048]]'s rule names. The blocker in decision 1 is aimed at
  `eventpg`: a live PostgreSQL schema, a migration profile and an operational surface, all
  shaped by what the first real aggregate needs. Phase 1 builds none of those, writes no SQL
  and ships no schema, so nothing it delivers is shaped by an unknown aggregate.
- **The doc obligation that comes with it.** `CLAUDE.md`'s "closed something the roadmap
  listed as open" rule makes `Roadmap.md`'s row 15 and §15's opening sentence part of the
  same change, and [ES]'s two E0 rows must be marked superseded in place, the way [[D-116]]
  superseded the multitenancy roadmap's module half. `scripts/roadmap_test.go` reads the
  roadmap and will need the same care.

---

## 4. [SPEC]'s open questions, answered against the tree

Only the questions whose answer moved, or whose answer needed evidence, are restated. Q7, Q8,
Q14, Q16, Q17, Q18 and Q20 are answered correctly in [SPEC] and the tree agrees; nothing is
added here.

**Q1 — the framework symbols for conflict, retryable and too-large; may `event` import them.**
Confirmed. `crud.ErrConflict` (`crud/errors.go:32`), `crud.ErrUnavailable` (`:38`), and
`errs.TooLarge()` (`errs/build.go:25`) building an `*errs.Fault` with `errs.KindTooLarge`,
because `port/kind.go:17:KindOfWith` consults `errs.AsFault` before `sentinelKind` and there
is **no** too-large sentinel anywhere. `tenancy/errors.go` is the precedent, [[D-116]] the
argument, and §2.4 measures the cost. `port/porthttp/errors.go:27:StatusFor` maps
`KindTooLarge → 413`, `KindConflict → 409`, `KindRetryable → 503`.

**Q2 — numbering and doc placement.** Next free: **D-121**, **FL-036**, **UC-032**. The
`docs/ai/usecases/` tree has no `UC-029` — it is a hole and taking it would be worse than
leaving it. The `UC-nnn`/`INV-nnn` numbering inside [SPEC] stays internal to that file. One
repository use case, `docs/ai/usecases/modules/event/UC-032-….md`, covers the consumer-visible
contract, links only to flows and names no symbol — `scripts/docs_test.go:526` enforces the
symbol half automatically. §2.3's sentinel table lands in `FL-036`, which is the only place
file paths and symbols may appear.

**Q3 — name collisions.** [SPEC]'s answer is right. Two additions from a fresh measurement:
`event.Failure` (a function) sits beside `jobs/durability.go:81:Failure` (a `uint8` enum) —
not `jobs.HandlerFailure` as §Q3 says; and `event.Aggregate` sits beside
`crud/aggregate.go:51:Aggregate`, a function producing an aggregation `Option`. Neither is a
Go collision and [[D-035]]'s rule covers both, but both are worth a line in the module page.

**Q4 — the transaction binding's concrete mechanism.** Confirmed and unchanged. `event` names
no executor vocabulary; the binder is store-private (`crud.WithExecutorFor` for SQL,
`eventmemory.WithTransaction` for memory). §2.3 verifies the two-step-form claim against
`crudsql.Transaction` (`crud/adapter/crudsql/crudsql.go:195`) and `crudsql.TransactionFor` (`:210`) and finds it
exactly true.

**Q5 — empty append.** Owner's, and the no-op is the answer to keep. The tree agrees where it
has the same question: `crud/executor.go:385:UnsafeBulkInsertFor` returns `(0, nil)` for zero
rows rather than refusing.

**Q6 — redundant revision numbers.** No, and §1.2 has the measurement: four validations in
`jobs/definition.go:341:normalizeUpcasters` exist only because the number can be wrong.

**Q9 — `*testing.T`.** Confirmed by `cache/cachetest/suite.go` and `check_deps`'s
`{{if not .Standard}}` filter.

**Q10 — the numbers.** §D.13's table cites `jobs/bounds.go` correctly for `MaxPayload`
(64 KiB), the kernel ceiling (1 MiB) and the identifier cap (128 B). What is still owed is a
real consumer's measurement for `MaxBatch`, `StreamPage`, `MaxRead` and `MaxKey`, and §D.13's
product arithmetic is the honest stand-in. `jobs.MaxSupportedRevisions = 8` transfers as the
retained-revision cap if a retention feature ever arrives (non-goal 13).

**Q11 — `SUBSYSTEMS` and the graph tests.** Yes to both. §3.9 has the detail, including the
`crud`+`errs` allowance the cost test needs and what is deliberately not fixed.

**Q12 — memory-store transactions.** Yes, against the tree's two precedents, for §3.8's
reason, with the fallback and its named debt spelled out so the owner can take the cheaper
answer knowingly.

**Q13 — "no goroutine started" and "no package-level mutable state".** The goroutine half is
`scripts/tenancy_test.go:TestTheExtensionDoesNothingWhenItIsMerelyImported`, copyable
verbatim. The mutable-state half exists nowhere and must be written; §INV-013's table is the
predicate and `scripts/docs_test.go`'s AST plumbing is what to build it from.

**Q15 — what the superseding decision must say.** D-121 in §7, and it must answer **both**
[ES] E0 rows, not one: decision 3 (no root package) is superseded, and decision 1 (the
aggregate blocker) is answered as out of scope for a phase that writes no schema.

**Q19 — the family collisions `Bind` cannot see.** The tree's candidate answer is real and
runnable: `jobs/catalog.go:25:NewCatalog(declarations ...Declaration)` collects every
declaration a deployment composes into one value, sorts by name, refuses a duplicate at
`:46`, and fingerprints the set at `:135` so a driver can refuse a schema written by a
different catalogue. `jobspg` takes a `Catalog` in its `Spec` and does not accumulate
declarations itself. An `event.NewCatalog` on that model sees every declaration one
*application* composes whichever `Binding` each went through, which closes the in-process half
of §INV-039's second escape. It is **additive** to phase 1 — `Bind` still refuses the
reachable case, and `eventtest.Families` is the runnable proxy until then — so it belongs in
`## Debt`, which is where §9 item 3 already puts it. The cross-*process* escape stays an
obligation; no mechanism inside one binary can see it.

**Q21 — panic-only, or the tree's pair.** **Closed by §3.7:** `event.Define` panics and
`event.TryDefine` returns the error, which is `crud/sqlrepo`'s own split and the one
[[D-021]] cites by line. Both documents get what they wanted.

---

## 5. Round 10 is open — what the plan inherits

[GAPS] round 10 (`:5308`) audited the current [SPEC] and did not receive a resolution round.
It closes with **Carry into the plan** (`:5994`), ten constraints, "the first three block the
gate". By `~/.claude/skills/econv/references/gaps.md` an `[immediate]` `high` blocks a gate,
so the plan cannot silently start section 1 with these open.

| # | Gap | Severity | What it is, in one line | Where it lands |
|---|---|---|---|---|
| 1 | GAP-100 | `high` | §INV-021's outbound enumeration covers what a codec *receives* and *encodes*, never what it *decodes* — a codec that decodes into a reused scratch corrupts every folded state, on the one path the kernel pays zero clones for | `event.Codec`'s contract, plus a `RoundTrip` assertion driven by a deliberately scratch-reusing codec |
| 2 | GAP-101 | `high` | A policing decorator — §UC-048's own precondition — has no way to spell its refusal, and the append door's fail-safe default turns it into `ErrUncertain` for a write never issued | one more `Outcome`, or a stated class for a policy refusal; §D.14's own compatibility paragraph makes adding an `Outcome` additive |
| 3 | GAP-102 | `high` | `Failure(Unconfirmed, ctx.Err())` wraps the cause, so `errors.Is(err, context.DeadlineExceeded)` answers **true** on an `ErrUncertain` — the caller's first branch swallows the unrecoverable fact | one stated answer, asserted in the `cancellation` section in both windows |
| 4 | GAP-103 | `medium` | §INV-018 says a store never applies a bound; §D.14 makes it apply `StreamPage` and `MaxRead`, and nobody verifies a longer page | a `bounds` case, an over-long-page defect, or §UC-011's *Observed* narrowed |
| 5 | GAP-104 | `medium` | `ErrTooLarge` on the **load** path and `ErrCursor` sit in classes their own use cases contradict | §INV-024's ground-truth table plus a transport-level assertion |
| 6 | GAP-105 | `medium` | The inbound enumeration claims totality and omits five calls, one of which retains an application callback (`eventmemory.New(Spec{Clock: …})`) | recompute the four/two/one count and re-check every citation of it |
| 7 | GAP-106 | `medium` | Rows 4 and 7 grant the recipient the right to write and never require the hand-out's **capacity** to be its own — a store sub-slicing one page buffer hands out slices whose `append` overwrites the next event | a full-slice expression obligation, and an `append`-one-byte conformance case |
| 8 | GAP-107 | `medium` | §INV-021's falsification names a **seventh** conformance decorator against a defect count of six asserted in five places | one count, one fixture inventory |
| 9 | GAP-108 | `high`→`medium` in effect | Nothing states a `Codec`, a mapper or an injected clock is safe for concurrent use, while §INV-038 promises the values holding them are | a stated obligation, or a recorded unpoliced one with the reason |
| 10 | GAP-109 | `low` | Six one-clause residuals, three in text an implementer reads as contract — §UC-005's reader list, §UC-068's `Ledger` fold (unguarded, panics on the zero map), and where `MaxKey` sits in `Append`'s order | six clauses |

**Three of these are contract-shaped and cannot wait for implementation.** GAP-101 changes
`Outcome`, which is the extension point's enum; GAP-102 changes what a caller may match;
GAP-100 changes `event.Codec`'s documented obligations. All three are §D.14 or §D.2 surface.
The plan's section 1 is where the contracts are written, so they must be closed **in the
plan's contract section**, not deferred to a later section that would rewrite it.

**Recommendation for the plan:** carry items 1–3 as blocking preconditions of the plan's
first contract section, carry 4–8 and 10 as contract clauses inside the sections that write
the value they concern, and carry 9 into the codec's own module page. Item 6's recount is a
five-minute edit to [SPEC] that the plan cannot do for it — flag it to the owner.

**One observation from round 10 worth keeping.** Its closing note (`:5982`) diagnoses six
findings of one shape across rounds 5–10: *the design contracts the values that cross a
boundary and does not contract the parties that hold state across calls.* The store was made
a party, the kernel was, the caller was; the **codec** was not. That is the same diagnosis
this reconciliation reaches from the other side in §2.2: `jobs.Codec` is a *stateful* object —
`jobs/json.go:16:jsonCodec[V]` holds `root`, `inline`, `mapLike`, `objectBytes`, `encodeWork`,
`trusted` and `chargeErr` — and `event.Codec[V]` will be one too. Whatever [SPEC] says about
the codec as a party is what `event.JSON`'s implementation has to satisfy.

---

## 6. Risks

**R1 — `make unit` fails on the docs, not on the code.**
`scripts/docs_test.go:526:TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` and
`:49:TestEveryTestNameTheDocsCiteExists` read every file under `docs/` and fail when a cited
`path.go:Symbol` or `TestName` does not exist. The new flow, the three module pages **and
their Russian translations** are all in scope. Every citation must be written after the symbol
exists. This is the most likely first red.

**R2 — round 10's three `high` findings are contract-shaped.** §5. If they are not closed in
the plan's contract section they will be closed by whoever implements `Outcome` first,
silently and differently.

**R3 — `eventmemory`'s transactions are the schedule risk.** Staged appends, re-validation at
commit, rollback leaving no readable event and no reusable position, all under `-race`, plus
the third `eventtest` fixture that needs its own staging (§UC-045's leaky-rollback store). If
phase 1 slips, this is where.

**R4 — the self-falsification suite has no precedent in the tree.** Six defects, four
forwarding decorators and two purpose-built stores, plus the rule that each fails the section
named for it and the three named exceptions (§UC-045, GAP-25). Nothing in `cachetest`,
`crudtest` or `test/integration` does this. Price it as new.

**R5 — the second copy of the JSON analyser is pre-existing debt this phase will be blamed
for.** An ECONV DRY audit of `event` will find `jobs/json.go` and `cache/codec.go` and read a
third codec as the third instance. §3.3's ADR item is what stops that conversation being had
three times.

**R6 — `scripts/event_test.go` fails on its first run if copied verbatim.**
`tenancy_test.go:TestNoTenancyPackageCostsMoreThanTheSeamItNames` computes the core's
first-party graph against `./crud`'s and allows exactly that. The `event` equivalent must
allow `crud` **and** `errs` explicitly, or it fails immediately and gets loosened under
pressure — which turns a structural check into a decorative one.

**R7 — the zero-diff check that cannot find its comparison point passes.** `git tag` is empty.
A check written against a tag that does not exist prints nothing and returns zero. §2.6's
first shape has no such failure mode; the git shape must print a loud skip.

**R8 — the exported-symbol breach is argued and will still be read as unargued.** §D.16
declares ≈ 71 exported symbols against `architecture.md`'s threshold of 7, with the four-group
argument. The tree agrees loudly — `docs/api/surface.md` shows `crud`, `jobs` and `cache` each
exporting dozens — but the plan must carry §D.16's per-type measurement (no exported type has
more than 8 methods; the one with 8 is a port with two consumable halves of 4) or a validator
will read the breach as unargued.

**R9 — `go.work` and `check_workspace`.** No new module means no `go.work` edit and no
`replace` lines in `test/go.mod` or `_examples/go.mod`. This is a risk in the negative sense:
an agent following [ES]'s `eventpg` checklist will add them, and `check_workspace`
(`scripts/checks.sh:305`) fails because `workspace_modules` discovers modules by `go.mod` and
finds none.

**R10 — `crudsql.Transaction`'s bare type assertion.** §2.3's first hazard. It does not reach
phase 1, and it decides whether `eventpg` can join a transaction behind a decorated executor.
Raise it as a `crudsql` finding now so phase 2 does not discover it as an `event` bug.

**R11 — `Roadmap.md` and [ES] go stale on the day this lands.** `docs/roadmaps/Roadmap.md:27` (row 15,
"no") and `:325` ("No event-sourcing or outbox package exists today"), plus [ES]'s E0
decisions 1 and 3 at `:558` and `:560`. `scripts/roadmap_test.go` reads the roadmap.

---

## 7. ADR agenda

Next free number is **D-121** (`docs/ai/decisions/` holds `D-001`…`D-120`). Four decisions, in
the order they must be written, each needing its row in `docs/ai/decisions/Index.md` in the
same change. All four land as `in force from phase 1` with a `Proven by (owed)` section if the
decision is written before the code, per `Index.md:31-40`.

---

**D-121 — The event vocabulary is a root package, and its second implementation is what earns
it.**

*Invariant:* `event`, `event/eventmemory` and `event/eventtest` are packages of
`github.com/frostgrove/vv`. `event` does not join `scripts/checks.sh:TIER0`. A store that
requires a driver becomes a module under `event/`, exactly as `jobs/jobspg` did.

*Must say:*
- [ES]'s "Decision in one page" item 4 and E0 decision 3 — *"No root `event` package is
  created for the vocabulary"* — are **superseded**, and [ES] is edited to say so in the same
  change, the way [[D-116]] superseded the multitenancy roadmap's module half.
- The argument is [[D-116]]'s, restated: a module boundary is a third-party dependency
  boundary, not an optionality boundary. `go list -deps ./event/...` names the standard
  library, `utils`, `crud` and `errs`, and nothing else. **`jobs`, the subsystem this copies,
  already names five first-party packages** — measured, not assumed.
- `event` is **not** on the [[D-048]] manifest and does not ask to be. [[D-048]]'s rule is
  about a package a third party writes *against* under `make check-tiers`; `event` is an
  ordinary root package with ordinary first-party dependencies, like `jobs`.
- The second-implementation rule [[D-048]] states is nonetheless **satisfied on day one**,
  which is why the store contract may exist at all: `eventmemory` is a complete
  implementation, `eventtest` is the concrete conformance obligation, `eventpg` is the third.
  **[ES]'s own E0 decision 3 says "a second store would reopen it"**, so this decision is that
  row's own condition being met rather than a contradiction of it.
- **Why the E0 aggregate blocker does not reach this phase** (§3.10). This paragraph is the
  one a future reader will come looking for.
- The three `crud` symbols and the one `errs` class, enumerated, and what each of
  `check-deps`, `check-tiers` and `check-utils` actually measures (§2.4) — so the next reader
  does not re-derive it.
- *What would reverse it:* a store implementation that cannot be written without `event`
  naming a driver's vocabulary, or a second consumer of the root module that must not compile
  `crud`.

*Where it lives:* `event/`, `scripts/checks.sh:SUBSYSTEMS`, `scripts/event_test.go`,
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`, `docs/roadmaps/Roadmap.md` §15
and its summary row.

*Proven by (owed):* `scripts/event_test.go:TestNoBaseSubsystemDependsOnTheOptionalExtension`,
`…:TestNoEventPackageCostsMoreThanTheSeamItNames`,
`…:TestTheExtensionDoesNothingWhenItIsMerelyImported`, and the zero-diff arm of §2.6.

---

**D-122 — A store classifies its own failure in a closed vocabulary, and the kernel maps it
through `errors.As`.**

*Invariant:* Every error crossing the store boundary in the store's direction carries exactly
one `event.Outcome` or none. The kernel's map from outcome to refusal row is total, fixed, and
the only thing it reads; it finds the outcome with `errors.As` and **never** with a type
assertion. A store adds no sentinel to §2.3 and needs no `event`-internal access to say any
of it.

*Must say:*
- The precedent, by name: `jobs/queue.go:799:RejectPlacement` is the same shape, and
  `jobs/queue.go:808:normalizeSenderError` already implements the fail-safe default with
  `jobs.ErrAmbiguous` as the answer to an unclassified failure of a write.
- **The one thing it does differently and why**: `normalizeSenderError` reads the
  classification with `err.(rejectedPlacement)`, so a forwarding decorator that wraps with
  `%w` loses it and a conflict becomes an ambiguity. `event` uses `errors.As`. Write this down
  as the reason, because it is the whole content of the rule and the next reader will find
  `jobs`'s version first.
- **Why the vocabulary is its own enum and not the sentinel set.** `jobs` reuses six sentinels
  as the classification, so `RejectPlacement` is a `switch` on pointers that degrades to
  `ErrInvalid`. `event.Outcome` is a defined `uint8` and the map between it and §2.3 is a
  table — one thing being classified, one thing being rendered.
- The door rule: what an **unclassified** error means differs between a write and a read
  (`ErrUncertain` from `Append`, `ErrBackend` from a read), a **classified** outcome is mapped
  the same way at every door, and a bare `context.Canceled` / `DeadlineExceeded` overrides
  every default (§UC-057's first window).
- Growth: adding an `Outcome` is additive because no store returned one that did not exist;
  removing one or changing what it maps to is breaking.
- Forbid: a kernel that inspects a store's error text, re-reads a stream to work out whether
  an append conflicted, or infers a classification from a driver code it does not import.

*Where it lives:* `event/`, `event/eventmemory/`, `event/eventtest/`, and phase 2's
`event/eventpg/`.

*Proven by (owed):* `eventtest`'s `store failure classification` section — one case per
`Outcome`, each driven again through a decorator that wraps the store's error with the
sentinel asserted unchanged; the unclassified pair at `Append` and at `ReadAll`; and the
trivial store in `eventtest`'s own test package returning a classified failure of every kind
with no `event`-internal access.

---

**D-123 — The declaration panics, and `Try…` is what returns the error.**

*Invariant:* `event.Define` and `event.Declare` panic on a malformed declaration, with an
error value carrying `ErrDeclaration` so a recovering test matches with `errors.Is`.
`event.TryDefine` and `event.TryDeclare` perform the identical checks and return
`(…, error)`. There is exactly one code path that builds a declaration.

*Must say:*
- The tree has three spellings and this takes the third: `crud/sqlrepo/blueprint.go:74:Define`
  panics and `:82:TryDefine` is the same without it, and [[D-021]] already names both, with
  `TestBadDeclarationsPanicEarly` for the panic and `blueprint_edge_test.go` for the refusals.
  The other two — `Define`/`MustDefine` (`jobs`, `app/module`) and `New…`/`Must…` (`cache`,
  `crud`, `port`) — make the *error-returning* call the plain name, which is what [SPEC]
  §UC-004 refuses and what the earlier reconciliation mistakenly recommended.
- Why the sibling exists, and it is not dynamic declaration: **the negative tests**. §UC-004
  lists nine triggers and §UC-056 adds a tenth; written against a panic they are ten
  `recover()` blocks, which is how a refusal check stops being read.
- Why §INV-013 survives: the two calls return the *same* value through the same code path;
  `Define` is three lines over `TryDefine`.
- Forbid: a second construction path, a `Define` that repairs a defect with a default, a
  declaration check deferred to a request, and any `Must…` alias that would make a third name
  for one thing.

*Where it lives:* `event/`, and [SPEC] §UC-004 and §D.12, both of which state the opposite and
must be edited in the same change.

*Proven by (owed):* the declaration refusal tests, written against `TryDefine`, with one
`recover()`-based case asserting `Define` panics with a value matching `ErrDeclaration`.

---

**D-124 — `event.JSON` is deliberately smaller than the codecs beside it.**

*Invariant:* `event`'s JSON codec is `encoding/json` plus a byte bound and a bounded
`CanEncode` walk. It carries no hardened scanner, no transient-work charge model and no
`json_mode_v*.go` build-tag pair.

*Must say:*
- The measurement: `jobs/json.go` (1407 lines) and `cache/codec.go` (1564 lines) share **20**
  identically-named unexported functions, and the `json_mode_v1.go`/`json_mode_v2.go` pair
  exists in three directories. A third copy would be the third.
- The threat-model argument: a job payload and a cache value arrive from a queue or a shared
  server and are adversarial input; an event payload arrives from this system's own
  append-only log behind a byte cap the kernel applies (§D.14's division of labour).
- What the smaller codec still guarantees: equal values encode to identical bytes, a type that
  cannot round-trip deterministically is refused at **declaration** rather than at a request
  (§UC-056, §INV-023), and the reflective walk carries its own depth and node bounds on
  `jobs/json.go:1062:discover`'s model.
- What the codec is as a *party*, not only as a function — what it may retain, what its
  `Decode` may alias, and whether it may be called concurrently (round 10's GAP-100 and
  GAP-108). This decision is the right home for that once [SPEC] settles it.
- **The trigger for extracting a shared stdlib-only analyser** — a fourth consumer, or the
  first defect fixed in one existing copy and not the other — so the conversation resumes
  rather than restarts.

*Where it lives:* `event/`, and `docs/modules/{en,ru}/event.md`.

*Proven by (owed):* `eventtest.RoundTrip`'s per-revision assertions, including the
scratch-reusing-codec case round 10's carry item 1 names; and the `CanEncode` refusal cases
with an accepting codec in the same test as their control.

---

*Also owed in the same change, and not decisions:* `docs/ai/flows/FL-036-….md` — the only place
file paths and symbols may appear, including §2.3's sentinel table and both index tables in
`docs/ai/flows/Index.md`; one repository use case
`docs/ai/usecases/modules/event/UC-032-….md`, linking only to flows and naming no symbol, plus
its row in `docs/ai/usecases/modules/Index.md`; three module pages in `docs/modules/en/` and
three in `docs/modules/ru/` with rows in `docs/modules/Index.md`;
`docs/roadmaps/Roadmap.md` §15 and its summary row 27; the two superseded E0 rows in [ES];
and `make api`.
