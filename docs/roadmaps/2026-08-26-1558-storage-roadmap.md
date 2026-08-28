# Storage roadmap — filesystem and S3-compatible object stores — 2026-08-26 15:58 +05

This roadmap proposes one application-facing object-storage contract with two
initial implementation families:

1. a filesystem-backed store for local development, tests and deliberately
   local deployments; and
2. an S3-compatible store for MinIO, Amazon S3, Cloudflare R2 and compatible
   services chosen by the consumer.

It is not a proposal to make files a CRUD resource or to hide every difference
between a POSIX filesystem and an object store. Those systems have irreducible
semantics: atomic rename versus multipart upload, local path permissions versus
remote credentials, immediate local reads versus provider consistency/retry
behaviour, object versions and pre-signed capabilities. The common contract is
therefore small and honest; capability-specific behaviour is explicit.

## Status and architectural fit

**Status:** proposed; no root storage subsystem or public storage satellite
exists yet.

The root module stays stdlib-only. A storage contract in the root would be
justified only if multiple root subsystems presently needed it; that is not true
today. Under [[D-048]] and [[D-051]], the likely shape is a storage satellite
whose core interface remains dependency-light, with adapters that isolate the
consumer's backend/SDK choice:

| Candidate module | Consumer choice isolated | Initial state |
|---|---|---|
| `vv/storage` | adopt vv object-storage contract | proposed satellite core |
| `vv/storagefs` | use filesystem semantics | stdlib-only adapter; may live with storage only if one decision |
| `vv/storages3` | choose an S3 SDK/compatible endpoint | separate satellite/adapter |
| `vv/storageminio` | choose MinIO-specific SDK/admin API | defer; S3 compatibility should suffice |
| `vv/storageaws` | choose AWS SDK extras | defer; do not impose AWS dependency |
| `vv/storager2` | choose R2-specific API/SDK extras | defer; S3 compatibility should suffice |
| `vv/storageotel` | observe storage through OTel | future bridge; follows OTel roadmap |

Whether `storagefs` remains in `vv/storage` is a dependency-decision review:
filesystem uses only standard library and does not create an external consumer
choice. It must still remain separately testable because its semantics differ.
An S3 adapter must never cause the core or fs-only user to import an AWS,
MinIO, Cloudflare or HTTP SDK.

## Product intent

The shortest correct application path should express an immutable object
identity, an explicit metadata/content contract and clear resource ownership:

```go
store, err := storage.Open(storage.Config{
    Namespace: "documents",
    // implementation comes from an explicit fs or S3 adapter
})

info, err := store.Put(ctx, key, content, storage.PutOptions{
    ContentType: "application/pdf",
    IfAbsent: true,
})
if err != nil { /* typed storage error; source remains caller-owned */ }
```

The final public names are intentionally undecided. The required properties are
not:

- caller owns `context.Context`, input `io.Reader` and returned `io.ReadCloser`;
- object bytes are streamed, never implicitly read into memory by the contract;
- key normalization and root containment are specified once, before adapters;
- each write's overwrite/conditional/versioning semantics are explicit;
- metadata is bounded and portable or consciously capability-scoped;
- pre-signed URLs are explicit S3 capability, never synthesized for fs;
- consistency/retry/encryption/retention claims are owned by the backend and
  surfaced as documented capability/error semantics;
- no backend URL, credentials, bucket or object key leaks through default logs,
  metrics or OpenTelemetry attributes.

## Source material and external semantics

The implementation must be tested against real provider behaviour, not merely
an emulator. These references inform the contract and test matrix:

- [Amazon S3 multipart upload](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)
- [Amazon S3 presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html)
- [Amazon S3 object versioning](https://docs.aws.amazon.com/AmazonS3/latest/userguide/AddingObjectstoVersioningEnabledBuckets.html)
- [MinIO S3 compatibility](https://min.io/product/s3-compatibility)
- [MinIO object versioning](https://min.io/docs/minio/linux/administration/object-management/object-versioning.html)

The roadmap will add direct R2 conformance references before adapter work begins.
R2 must be accepted as an S3-compatible target only for operations it documents
as compatible; avoid silently treating “S3 API accepted the request” as exact
AWS feature parity.

## Vocabulary and boundaries

| Term | Meaning |
|---|---|
| namespace | configured logical collection owned by application, not necessarily bucket |
| backend container | filesystem root or S3 bucket chosen in adapter configuration |
| key | opaque, normalized application object identifier within a namespace |
| object | immutable bytes plus portable metadata returned by a successful write |
| version | backend-issued opaque revision where configured/supported |
| ETag/validator | backend opaque validator; never assumed to be content hash |
| capability | optional operation whose availability is declared at startup |
| staged object | temporary object not yet promoted into its application-visible key |
| source | caller-provided `io.Reader` for writes |
| body | caller-owned returned `io.ReadCloser` for reads |

No generic `File` type carries arbitrary platform path, URL, reader, writer,
metadata map and S3 response at once. Such a type encourages memory ownership
confusion, backend leakage and accidental logging. Inputs and results must be
small, typed and operation-specific.

## Non-negotiable invariants

1. **Opaque logical keys.** The core does not expose physical filesystem paths,
   S3 URLs, bucket names or SDK object handles as object identity.
2. **Streaming first.** `Put` consumes source incrementally; `Get` returns a
   closable stream. Byte-slice helpers, if added, have explicit size limits.
3. **Caller-owned resources.** Caller closes source if it owns it; caller closes
   returned body exactly once. Implementations clean temporary resources on
   every error path.
4. **Fail closed on unsafe keys.** Empty, absolute, traversal, ambiguous slash,
   control-character and over-limit keys are refused before backend contact.
5. **No accidental overwrite.** Default write behaviour is chosen explicitly;
   “last writer wins” cannot be implicit just because a backend allows it.
6. **Conditional writes are portable where claimed.** If a backend cannot
   faithfully implement `IfAbsent`/validator semantics, it exposes no false
   success and capability check fails at construction.
7. **No hidden durability claim.** Successful `Put` means only the configured
   backend accepted the contract's documented completion point; fsync/replica/
   cross-region guarantees are named separately.
8. **No unbounded metadata.** Metadata keys, values, count and total bytes are
   validated against fixed portable limits before write.
9. **Context governs cancellation.** Every remote/local blocking operation uses
   caller context where underlying API permits; cancellation result is not
   relabelled as successful cleanup.
10. **Backend errors project once.** A typed storage error is portable without
    guessing provider text; raw SDK errors are available only through controlled
    unwrap/diagnostic policy if the public contract says so.
11. **No credentials in domain objects.** Configuration belongs to adapter
    bootstrap; no secret endpoint/access key/pre-signed URL appears in errors,
    metrics or default telemetry.
12. **Delete does not prove erasure.** Versioned/object-lock/retention systems
    can retain prior bytes. API names must distinguish delete marker, purge and
    application-level tombstone where relevant.

## Operation surface proposed for the first core

| Operation | Core? | Completion meaning | Key risk |
|---|---|---|---|
| `Put` | yes | source accepted according to write mode | overwrite/race/multipart |
| `Open` | yes | readable body and portable info obtained | caller must close body |
| `Head` | yes | existence/portable metadata obtained | no bytes streamed |
| `Delete` | yes | delete request applied according to mode | versions/retention |
| `Copy` | maybe | backend/server or streamed copy completed | source/destination condition |
| `Move/Promote` | staged capability | new visible key established | fs rename vs object copy+delete |
| `List` | defer | paged enumeration | huge namespaces and consistency |
| `SignGet`/`SignPut` | S3 capability | signed request created | credential-bearing URL |
| `Multipart` | adapter internal initially | large put completed | abort/retry/parts |
| `Version`/restore | capability | backend version operation | nonportable behaviour |
| object locking/retention | defer | provider-specific governance | false compliance claim |

The first release must resist convenience pressure for `List`. Namespace scans
are where delimiter semantics, pagination/cursors, prefix disclosure, enormous
cardinality and backend divergence appear. Build `Head` and explicit application
metadata/index flows first; add `List` only after a complete visibility and
pagination contract exists.

## Error taxonomy

Storage errors must be operations plus bounded conditions, not a flattened
string. The final implementation can map them into existing `errs` only where
the project wants shared public codes; it must preserve storage diagnostics for
operators without serializing raw provider text to clients.

| Condition | Meaning | Retry default | Public information |
|---|---|---|---|
| invalid key/metadata/options | rejected before side effect | no | field/category only |
| not found | visible object absent at documented read point | no | key withheld by default |
| already exists | conditional create saw existing object | no | no validator/key leak |
| precondition failed | supplied validator/mode did not match | no | condition class only |
| forbidden | caller/backend not authorized | no | generic refusal |
| unsupported | configured store lacks requested capability | no | capability name |
| conflict | concurrent promotion/version operation lost | caller decision | bounded mode |
| cancelled/deadline | context ended | caller decision | standard context class |
| temporary | backend reports safe retryable fault | explicit caller policy | provider class only |
| unavailable | backend cannot serve now | explicit caller policy | backend family only |
| corrupt | read integrity check failed, if enabled | no automatic retry | integrity class |
| internal | unexpected adapter/backend failure | explicit caller policy | no raw message |

No adapter retries writes invisibly after uncertain network failure. It cannot
know whether remote object creation succeeded; caller must use a conditional key,
idempotency token/application record, or an explicitly designed retry wrapper.

## Compatibility matrix before any public API

| Behaviour | fs | S3/MinIO/AWS/R2 | Core promise |
|---|---|---|---|
| bytes stream in/out | yes | yes | required |
| `IfAbsent` atomicity | filesystem primitive/locking needed | conditional request capability | required or refuse adapter mode |
| overwrite with validator | local stat/atomic replacement design | conditional request/ETag semantics | only if exact semantics proven |
| atomic move | same filesystem rename possible | copy + delete, non-atomic | staged `Promote`, not generic move |
| version history | custom/not native | optional provider versioning | capability, never default |
| signed URL | no | yes | S3 capability only |
| multipart | local stream/temp file | native service protocol | internal implementation detail |
| object retention lock | filesystem policy differs | optional provider feature | out of first core |
| directory semantics | physical implementation detail | prefixes only | no core directory API |
| list consistency | filesystem-dependent | provider/pagination-dependent | deferred |

## Delivery sequence

1. write core types, key grammar, error taxonomy and resource ownership tests;
2. implement secure filesystem store with an adversarial path/race test suite;
3. design a conformance suite independent of SDK;
4. implement one S3 SDK adapter selected by consumer, test against MinIO and
   AWS/R2 compatibility fixtures; do not claim broader support until verified;
5. introduce staged promotion, integrity options and bounded metadata;
6. add signed URLs/versioning/list only as capability-specific extensions;
7. integrate telemetry, audit, events and tenancy through narrow cross-roadmap
   contracts after their independent correctness gates are met.

---

## S-01 — bootstrap, namespaces and explicit backend ownership

**Decision.** Storage construction accepts a configured adapter, one validated
logical namespace and explicit defaults. It does not infer root/bucket from
model type, current directory, environment variable or URL string.

### Top-level declarative DX

```go
documents, err := storage.New(&storage.Config{
    Namespace: "documents",
    Backend: storagefs.New(&storagefs.Config{Root: dataRoot}),
})
```

### Happy use cases

1. A test creates an fs adapter rooted at a temporary explicit directory and a
   `documents` namespace. Every logical key is contained below that root.
2. Production creates an S3 adapter with a configured bucket/endpoint/credential
   provider owned by application bootstrap and passes it to storage core.
3. One process has `documents` and `exports` stores with separate namespaces and
   retention/configuration, with no global default store.
4. A service starts with invalid namespace or incompatible adapter capabilities;
   construction fails before serving a request.
5. An app composes storage adapter with OTel/audit decorators explicitly; base
   storage works unchanged without either optional satellite.

### Edge use cases

1. Relative fs root is supplied. Construction either canonicalizes it under a
   documented process policy or refuses it; it never depends on later `chdir`.
2. Namespace includes a customer name or tenant ID. Validation refuses or maps
   it to a configured bounded logical family; dynamic namespace is not a metric
   label and must be reviewed for isolation separately.
3. S3 bucket does not exist, credentials fail or endpoint is malformed. Adapter
   validates reachable capability only if bootstrap policy permits network I/O;
   otherwise first operation returns bounded unavailable/forbidden error.
4. Two stores accidentally point at same backend container with overlapping
   namespace prefix. Configuration requires an explicit overlap acknowledgement
   or rejects it; hidden collision is unacceptable.
5. An application creates a store per request. API allows it functionally but
   docs and benchmarks make connection/client lifecycle ownership explicit.
6. A test uses environment credentials by accident. Adapter test helper requires
   explicit credential source and rejects ambient production variables.

### Invariants and acceptance evidence

- core package imports no AWS/MinIO/Cloudflare SDK and no global configuration;
- namespace grammar has length/character/reserved-word tests;
- adapters expose immutable configuration after construction;
- two stores in one process prove no cross-root/key collision;
- bootstrap failure has no partial object write or background goroutine leak.

### First implementation slice

Define opaque `Namespace`, `Key`, `Store`, `Info`, `Capabilities` and typed
error values. Build an in-memory conformance fake only for tests; do not call it
a production backend or let it define fs/S3 durability semantics.

---

## S-02 — key grammar, normalization and containment

**Decision.** A logical key is neither a filesystem path nor an S3 URL. It has
one byte-level grammar and canonical representation before any adapter sees it.
The fs adapter maps it safely below a configured root; S3 adapter maps it below
an explicitly configured prefix. No adapter performs “helpful” second parsing.

### Top-level declarative DX

```go
key, err := storage.ParseKey("invoices/2026/statement.pdf")
if err != nil { return err }
info, err := documents.Put(ctx, key, source, options)
```

### Happy use cases

1. `invoices/2026/statement.pdf` parses to one canonical logical key and maps
   below fs root or S3 configured prefix without exposing either physical form.
2. A generated UUID key such as `uploads/01H...` retains slash-delimited logical
   grouping without creating a core directory API.
3. Application uses opaque encoded IDs as segments. They are validated for
   allowed alphabet/length and never decoded by storage.
4. A key supplied in a command is rejected before stream creation/backend call
   when it violates grammar, making error behaviour cheap and deterministic.
5. A copy/promotion uses already parsed source/destination keys, preventing a
   second inconsistent normalization path.
6. `Head`, `Open`, `Put`, `Delete` and future signed URL operations all share
   the same opaque key type rather than accepting raw strings inconsistently.

### Edge use cases

1. Key is empty, `/absolute`, `../escape`, `a/../../b`, `a//b`, `a/./b` or ends
   with separator. Each has a documented refusal/canonicalization rule; choose
   refusal wherever two strings could refer to same object unexpectedly.
2. Key contains backslash. Treat it as forbidden or ordinary literal according
   to one portable rule; never let Windows fs semantics reinterpret it as path.
3. Key has URL encoding, Unicode normalization variants or invisible/control
   characters. Contract selects accepted UTF-8/ASCII form and maximum bytes;
   it must not normalize differently on OS and S3 adapters.
4. Key contains `.`/`..` as a segment, device-like names, trailing spaces or
   case variants that collide on a case-insensitive filesystem. The fs portable
   mode refuses unsafe forms even if a Linux test happens to accept them.
5. Prefix configuration ends in slash while key starts in slash. Both are
   validated/canonicalized at construction; string concatenation cannot escape
   a backend container or produce double-prefix ambiguity.
6. Attacker sends a 20-MB key. Parser applies byte limit before path join, log
   formatting, metric/trace creation or allocation proportional to segments.
7. An application requires opaque binary object IDs. It encodes them into the
   approved grammar; storage does not accept raw arbitrary bytes as path data.

### Grammar proposal

The exact grammar needs an ADR before public release. A conservative portable
candidate is UTF-8 text limited to 1–1024 bytes, slash-separated 1–128 byte
segments, no empty/dot/dot-dot segment, no leading/trailing slash, no NUL/control
code point, no backslash, and no platform-reserved forms. It is intentionally
stricter than S3 object-key permissiveness because filesystem portability and
security define the common core.

### Invariants and acceptance evidence

- property test proves parsed key cannot contain traversal/absolute ambiguity;
- fs mapping test proves `filepath.Rel(root, resolved)` never escapes root;
- Windows/case-insensitive fixture or static conservative corpus is required,
  even if CI primarily runs Linux;
- S3 prefix join fixture proves no double/escaped prefix route exists;
- every operation accepts `Key`, not a raw user string after first public layer;
- error/telemetry fixtures prove rejected key never appears in default outputs.

### First implementation slice

Implement parser plus an exhaustive corpus before `storagefs` writes bytes.
Make `Key` immutable/opaque with a safe diagnostic representation that does not
default to raw `String()` in logs. A caller can retain original key separately
where its own authorization and audit policy permits it.

---

## S-03 — streaming put, read and resource ownership

**Decision.** The common store moves byte streams. It never closes a caller's
write source, never buffers an unbounded object, and returns a read body whose
close semantics are explicit. Metadata is available before/with `Open` without
forcing complete download.

### Top-level declarative DX

```go
src := bytes.NewReader(pdf)
info, err := documents.Put(ctx, key, src, storage.PutOptions{
    ContentType: "application/pdf",
    Size: int64(len(pdf)), // optional/verified contract, not required buffer
})
if err != nil { return err }

body, info, err := documents.Open(ctx, key)
if err != nil { return err }
defer body.Close()
_, err = io.Copy(destination, body)
```

### Happy use cases

1. A 10-GB input reader streams to fs/S3 without core allocating object-sized
   memory; adapter honours context and reports terminal result.
2. A caller provides known content length. Adapter uses it for validation/
   protocol optimisation but verifies/handles mismatch according to contract.
3. A caller omits size. fs streams to a temp destination; S3 adapter selects
   documented single/multipart strategy without `ReadAll`.
4. `Open` returns a readable body plus `Info` containing safe portable fields:
   content length, content type, creation/update value where meaningful,
   opaque validator/version if capability promises it, and user metadata copy.
5. `Head` obtains same metadata without opening a body; client can check a
   conditional/download decision before streaming large bytes.
6. A caller reads first bytes then closes body. Adapter releases file handle/
   HTTP connection/SDK response promptly.
7. A consumer wraps `Put` with a checksum reader owned by application; storage
   receives ordinary source and does not impose one hashing algorithm globally.

### Edge use cases

1. Reader returns `(0, nil)` repeatedly. Adapter protects against busy loop per
   `io.Reader` conventions and returns a bounded internal/source error.
2. Reader errors after remote multipart parts were accepted. Adapter aborts
   temporary multipart/session when possible, reports source failure and never
   reports successful visible object.
3. Reader yields more/fewer bytes than declared size. Exact policy is defined:
   fail safely and clean staging, or support adapter protocol requirements; it
   cannot silently record false `Info.Size`.
4. Context cancels during fs copy/S3 upload. Adapter stops work where possible,
   cleans temporary content according to documented best effort and returns
   cancellation without claiming remote absence if outcome is uncertain.
5. Destination/client stops reading returned body. Caller owns `Close`; adapter
   must document whether close drains/cancels underlying HTTP stream and test it.
6. A return body is closed twice or read after close. Standard Go semantics are
   preserved/documented; no panic or hidden retry occurs.
7. An object is modified/deleted after `Head` before `Open`. Conditional open
   requires explicit validator option; ordinary two-call read has usual race.
8. Object has untrusted content type/metadata from a prior writer. Store returns
   bytes/metadata faithfully within limits; application decides browser serving
   headers, sniffing and content safety.

### Stream contract questions requiring decision

| Question | Proposed conservative answer |
|---|---|
| Does `Put` close source? | never |
| Does `Open` body require close? | always |
| Is `Info.Size` known before put? | optional input, verified/result when known |
| Are reads seekable/range-capable? | not in first core; capability later |
| Are writes resumable? | adapter internal only; not first core |
| Does cancellation prove no object? | no; return bounded uncertain/temporary class if needed |
| Can `Open` return body plus error? | no; all-or-nothing result tuple |
| Is metadata map mutable? | return defensive copy/typed immutable representation |

### Invariants and acceptance evidence

- allocation benchmark grows with buffer size, not object size;
- custom reader/body fakes assert exact source/body close ownership;
- cancellation and source error tests prove no reported successful `Info`;
- content-length mismatch fixtures work under fs and S3 conformance adapters;
- partial read/close test checks no descriptor/connection leak under repetition;
- returned metadata cannot mutate adapter-owned cached state.

### First implementation slice

Define `PutOptions`, `Info`, `Open`/`Head` result shapes and a small streaming
fake. Implement fs write via a uniquely created staging file plus atomic final
placement only after S-04 decides write modes; never write directly to visible
path on first copy attempt.

---

## S-04 — write modes, idempotency and conditional concurrency

**Decision.** A write's collision semantics are requested explicitly. The
first core should support `CreateOnly` (object must not exist) and a clearly
named unconditional replacement only if adapters can state completion/race
semantics. Compare-and-swap/validator replacement is a capability, not guessed
from ETag or filesystem timestamp.

### Top-level declarative DX

```go
created, err := documents.Put(ctx, key, source, storage.PutOptions{
    Mode: storage.CreateOnly,
})
// Existing object -> typed AlreadyExists, never silent overwrite.
```

### Happy use cases

1. A user upload chooses `CreateOnly` for an application-generated immutable
   key. Repeated request receives `AlreadyExists`, allowing app idempotency to
   decide whether existing metadata represents same logical upload.
2. A cache regeneration explicitly chooses `Replace` and stores a new object;
   docs state that last successful writer wins only for this chosen mode.
3. A future `ReplaceIfMatch` uses a validator returned by `Head`/`Put` when the
   backend capability explicitly proves conditional semantics.
4. Two fs writers race on `CreateOnly`; exactly one becomes visible and the
   other gets existing/precondition failure without corrupt partial bytes.
5. Two S3 writers race on conditional create; adapter uses backend mechanism or
   refuses that mode at construction if it cannot provide the promised result.
6. A write retry after a timeout reuses CreateOnly key and can safely inspect
   `AlreadyExists` rather than blindly issuing an overwrite.

### Edge use cases

1. Network response is lost after S3 accepted a put. Adapter returns uncertain
   failure, not `ok`; application resolves with `Head`/validator/idempotency
   record according to its domain contract.
2. Filesystem temp file is created but process crashes before final placement.
   Startup/maintenance cleanup has bounded naming/root policy and never deletes
   arbitrary user objects based on a loose prefix.
3. Replace involves a file on another filesystem. fs adapter either stages under
   same root/filesystem or returns unsupported; cross-device copy+delete cannot
   masquerade as atomic replace.
4. ETag is multipart/non-content-hash. Contract calls it opaque validator and
   does not advertise checksum or portable strong CAS unless tested.
5. Backend lacks conditional request support. Adapter declares `CreateOnly` or
   `CompareAndSwap` unavailable rather than emulating with unsafe Head-then-Put.
6. Object lock/retention forbids delete/replace. Adapter maps provider response
   to forbidden/precondition condition without discarding governance meaning.
7. Writer supplies `CreateOnly` plus `ReplaceIfMatch`. Options validator refuses
   contradictory modes before source read/backend traffic.

### Invariants and acceptance evidence

- `PutOptions` contains one validated write mode, not boolean combinations;
- fs race suite uses concurrent processes/goroutines where platform supports it;
- S3 adapter conformance suite tests conditional request against real MinIO and
  selected cloud targets before public compatibility claim;
- network uncertainty case never returns a fabricated success/version;
- staging cleanup cannot escape root or remove non-owned files;
- docs distinguish idempotency key/application record from storage key collision.

### First implementation slice

Ship `CreateOnly` first. Defer unconditional replacement and compare-and-swap
until fs and S3 semantics have written conformance examples. A “convenient”
`Overwrite bool` option is explicitly rejected: it obscures the most important
data-loss decision in the API.

---

## S-05 — portable, bounded metadata and integrity signals

**Decision.** Metadata is a small portable map with a strict grammar and total
budget. It records application-declared non-secret facts such as content type or
an application-safe disposition class; it is not a JSON blob, claims bag,
authorization store or replacement for database metadata.

### Top-level declarative DX

```go
info, err := documents.Put(ctx, key, source, storage.PutOptions{
    ContentType: "image/png",
    Metadata: storage.Metadata{
        "classification": "public-thumbnail",
    },
})
```

### Happy use cases

1. A PNG object carries validated content type and one low-risk classification
   metadata entry; `Head` and `Open` return a defensive copy.
2. An application stores domain record ID in its database, not storage metadata;
   storage object remains portable if moved between fs and S3.
3. A caller supplies an optional expected checksum through a future integrity
   extension. Adapter verifies only documented algorithm/representation and
   returns a bounded corruption result on mismatch.
4. S3 user metadata encoding/casing is normalized at adapter boundary to core
   grammar, preventing provider-specific metadata keys leaking upward.
5. A file upload uses content type but application separately controls download
   response headers/content disposition to avoid browser security confusion.

### Edge use cases

1. Metadata includes `authorization`, `cookie`, `tenant_id`, user e-mail or a
   signed URL. Core policy refuses reserved/sensitive key names or application
   must keep them in its protected database/audit system.
2. Value is 1 MB or map has 10,000 keys. Validator rejects before buffering or
   S3 header encoding; total byte/count budgets are fixed.
3. Metadata key casing differs (`Foo` vs `foo`). Contract selects canonical
   lowercase grammar or rejects ambiguity consistently across adapters.
4. Backend adds system metadata (ETag, last modified, version). Core exposes
   documented typed portable fields, not a provider raw response map.
5. Content type is empty or malformed. Validation either permits explicit empty
   as unknown or refuses invalid non-empty string; no automatic sniffing reads
   the entire stream in a generic store.
6. A checksum reader detects mismatch after remote write. Adapter must abort/
   delete only if it can safely identify its staging object; it cannot delete a
   visible concurrent object by key as cleanup.

### Invariants and acceptance evidence

- metadata parser validates key/value/count/total size before source transfer;
- hostile metadata corpus is absent from errors, default telemetry and logs;
- fs/S3 round-trip tests preserve only portable approved metadata;
- `Info` separates system fields from user metadata and copies maps defensively;
- integrity feature never assumes ETag is a content hash.

### First implementation slice

Support content type and strictly bounded user metadata only. Defer checksum,
content disposition, tagging, ACL, encryption headers and legal retention until
their portable semantics and data-governance implications have dedicated cards.

---

## S-06 — secure filesystem adapter

**Decision.** The fs adapter is a real backend with explicit security and
durability rules, not a `filepath.Join(root, key)` helper. It must defend root
containment across symlinks/races, stage writes under the same filesystem and
make permissions/umask behavior explicit.

### Top-level declarative DX

```go
backend, err := storagefs.New(&storagefs.Config{
    Root: "/srv/app-data/documents",
    FileMode: 0o640,
    DirMode:  0o750,
})
```

### Happy use cases

1. A root owned by the application is supplied explicitly; `Put` creates only
   needed parent directories under it with configured conservative modes.
2. A write streams to a uniquely named staging file under the root filesystem,
   flushes/renames according to documented durability option and exposes final
   path only through opaque key.
3. `CreateOnly` obtains exclusive final placement semantics without a head-then-
   write race; concurrent writers observe one success and one existing result.
4. `Open` validates resolved object remains inside root, opens read-only and
   returns a closable file body with portable `Info`.
5. A test runs fs store in a temporary directory and reaches conformance parity
   with S3 for common Put/Open/Head/Delete cases.
6. Maintenance cleans only known staging entries after a crash, with age and
   ownership validation, leaving application-visible object keys untouched.

### Edge use cases

1. An attacker creates a symlink within root pointing outside between path
   checks and open. Adapter must use platform-safe open/rename strategy or
   refuse platform/mode where it cannot enforce containment.
2. Final object is a symlink/hard link created by another local actor. Security
   model names required root permissions and tests reject unsafe follow behavior.
3. Root itself is replaced after adapter construction. Adapter documents inode/
   handle behavior and refuses/contains writes; it never trusts cached string.
4. Filesystem is full, read-only, quota-limited or has I/O error. Adapter maps
   condition without retaining an open staging descriptor or claiming success.
5. `fsync` succeeds for file but directory sync fails. Durability configuration
   reports bounded error/uncertain state; docs distinguish visibility from crash
   persistence.
6. Windows or network filesystem lacks assumed rename/open semantics. Adapter
   declares supported platforms/filesystems and runs conservative mode otherwise.
7. A direct local administrator changes file permissions/content. Store exposes
   observed error/info but cannot guarantee external tampering detection without
   explicit integrity capability.

### Invariants and acceptance evidence

- security tests include traversal, symlink swap, root replacement and staging
  cleanup attacks, skipped only with documented platform reason;
- no visible final object contains partial bytes after failed/cancelled stream;
- file/dir modes are configurable with safe defaults and tested under umask;
- fsync/rename guarantees are documented per selected durability mode;
- staging naming cannot be confused with a valid parsed logical key;
- fs adapter performs no network and has no ambient working-directory dependency.

### First implementation slice

Start with a POSIX-supported secure mode and clearly reject unsupported root
conditions. Do not market it as cross-platform until symlink/race invariants are
validated on each advertised platform. A simpler test-only fake remains separate
from this security-sensitive adapter.

---

## S-07 — S3-compatible adapter without provider lock-in

**Decision.** The S3 adapter selects one SDK at module level but targets the
documented common S3 API subset. Endpoint, region, credential chain, transport,
retry, TLS and bucket are caller/bootstrap owned. MinIO, AWS and R2 are tested
targets, not imported product identities in the core contract.

### Top-level declarative DX

```go
backend, err := storages3.New(storages3.Config{
    Client: client, // caller owns SDK construction and credentials
    Bucket: "acme-documents",
    Prefix: "production/documents",
})
```

### Happy use cases

1. Application constructs its S3 client with AWS credentials or custom MinIO/R2
   endpoint, then injects it. Storage module performs no ambient credential
   resolution, env read or global SDK configuration.
2. `Put` uploads a small stream with portable metadata/content type and returns
   opaque validator/version fields only when available.
3. A large unknown-size source uses adapter's documented multipart strategy,
   completes/aborts according to source/context outcome and exposes one logical
   core result.
4. `Open` returns an SDK response body as a core `io.ReadCloser`, and `Close`
   returns connection resources to the SDK transport.
5. `CreateOnly` uses a real conditional/create mechanism tested against selected
   target; if a target lacks it, capability is absent rather than emulated.
6. Endpoint and path/virtual-host addressing quirks live solely in client setup;
   logical storage keys remain unchanged under AWS, MinIO and R2 configuration.

### Edge use cases

1. S3 response disappears after upload completion. Adapter returns uncertain/
   temporary failure and preserves no invented final `Info`; caller reconciles.
2. Multipart upload has accepted parts then source error/cancel. Adapter aborts
   using its upload ID when safe and reports abort failure as diagnostic context
   without replacing the primary source/cancellation result unpredictably.
3. Bucket versioning is disabled/enabled mid-deployment. Core continues opaque
   version semantics; capability tests/docs state exact return behavior.
4. ETag changes format with multipart/encryption/provider. Adapter returns it
   only as opaque validator and never validates content checksum from it.
5. Provider returns XML/HTTP raw error with secret request URL. Adapter maps a
   typed condition and retains raw response only behind controlled unwrap policy.
6. A configuration uses HTTP endpoint or insecure TLS for local MinIO. It must
   be an explicit caller choice, surfaced in application security checklist,
   never silently enabled by adapter defaults.
7. Credentials expire during a stream. Adapter reports bounded forbidden/
   unavailable class based on safe SDK classification; it does not retry an
   uncertain write automatically with a newly acquired credential.

### Invariants and acceptance evidence

- core/storagefs import no selected S3 SDK; only `storages3` does;
- target conformance suite runs real MinIO plus declared AWS/R2 tests before
  each compatibility statement is published;
- tests cover small put, multipart success/error/abort, open close, delete,
  conditions, metadata and provider raw-error redaction;
- client construction/count/lifecycle stays caller-owned and is injectable;
- no endpoint, bucket, object key, version ID or request URL enters defaults.

### First implementation slice

Pick one Go S3 SDK only after an ADR comparing its conditional requests,
multipart cancellation, custom endpoints and error types. Ship `Put/Open/Head/
Delete` conformance before pre-signed URLs or version operations. Do not add an
AWS session helper, MinIO admin client or R2-specific wrapper to this adapter.

---

## S-08 — multipart, staging and promotion

**Decision.** A logical visible object becomes visible only at a defined commit
point. fs uses same-filesystem staging plus placement; S3 uses multipart/session
completion or a staged-key plus server-side copy/promotion. Because remote copy
and delete are not atomic rename, promotion is a named capability with recovery
states, not `Rename` pretending to be portable.

### Top-level declarative DX

```go
stage, err := documents.Stage(ctx, source, storage.StageOptions{Mode: storage.CreateOnly})
if err != nil { return err }
defer stage.Abort(context.Background())

info, err := stage.Promote(ctx, finalKey)
```

### Happy use cases

1. A producer streams an object to a private staged identity, validates domain
   state, then explicitly promotes to one final logical key.
2. Fs promotion uses atomic same-filesystem placement when conditions permit,
   leaving no briefly visible partial final file.
3. S3 staged promotion uses documented copy/conditional finalization; it records
   final success only after final object contract completion, then cleans staging
   best effort according to recovery state.
4. A transaction/outbox workflow persists staging reference and can resume or
   compensate after process crash through an explicit maintenance process.
5. Large upload uses native multipart internally but client still receives a
   simple final `Put` for use cases that do not need domain-level staging.
6. Promotion returns portable final `Info`; stage handle becomes terminal and
   cannot be promoted/aborted twice silently.

### Edge use cases

1. Crash after final copy but before staging cleanup. Reconciler identifies
   owned stage by opaque record/state, not prefix scan alone, and deletion is
   idempotent/bounded.
2. Crash before final promotion. Staged bytes may exist; application must not
   serve them via public final key and maintenance retention policy cleans them.
3. Destination final key becomes occupied between stage and promote. Chosen
   conditional mode produces already-exists/precondition failure, never silent
   replacement.
4. S3 copy succeeds but delete staging fails due object lock/transient outage.
   Promotion reports final success plus controlled cleanup state only if contract
   makes cleanup nonessential; otherwise error semantics are explicitly decided.
5. A caller loses stage token/handle. It cannot reconstruct it from guessable
   object key; persistent domain record/audit owns authorized recovery access.
6. Multipart upload ID leaks into logs/telemetry. Adapter treats it as secret
   operational identifier and suppresses it from all default outputs.
7. `Abort` context is cancelled. Adapter makes documented best-effort cleanup
   and returns cancellation/cleanup condition without deleting a final object.

### Invariants and acceptance evidence

- no core method uses name `Rename` until atomicity semantics are portable;
- state diagram and crash injection suite cover every stage/promote/cleanup edge;
- staging references are unguessable/opaque and bounded in lifetime;
- promotion conditional semantics use final key exact mode, no unsafe head-put;
- cleanup cannot delete a final object or another process's staging resource;
- storage audit/telemetry bridges record operation/state class, never stage IDs.

### First implementation slice

Defer public staging until direct Put is secure. First prototype fs/S3 staging
behind internal interfaces and run crash tests. Publish only if a real domain
use case needs atomic relationship between object visibility and database/event
state; otherwise keep complexity out of common API.

---

## S-09 — signed URLs are an explicit S3 capability

**Decision.** Pre-signed GET/PUT URLs are temporary bearer capabilities created
by an S3-compatible backend. They are not portable object references, must never
be returned by ordinary `Info`, and have explicit key/method/expiry/content
constraints. Filesystem backend reports this capability unsupported.

### Top-level declarative DX

```go
url, err := storage.SignPut(ctx, documents, key, storage.SignPutOptions{
    ExpiresIn: 10 * time.Minute,
    ContentType: "image/png",
})
```

### Happy use cases

1. An authenticated application authorizes a user, creates a short-lived signed
   PUT for one prevalidated key and expected content type, then stores the URL
   only in its response/session path.
2. A download endpoint creates a short-lived signed GET after domain policy
   checks; authorization remains application-owned and is not delegated to key
   obscurity or an object ACL.
3. An S3 adapter injects its client/credential signer explicitly and validates
   expiry minimum/maximum at the core capability boundary.
4. Application records signed-request issuance in its audit system with actor/
   object subject under access control; storage telemetry sees only operation
   outcome and never the URL/key/credential signature.
5. Fs store exposes `Capabilities().SignedURL == false`; application follows an
   alternate streaming endpoint rather than a fake `file://` capability.

### Edge use cases

1. Caller asks for 30-day URL. Core refuses beyond policy max; a longer sharing
   workflow needs a durable application authorization/resource design.
2. A signed URL appears in a raw error, log, trace, referrer or browser history.
   Adapter/default telemetry never formats it; host docs require redaction and
   response header/referrer policy review.
3. Required S3 headers/content type differ at browser upload time. Adapter
   returns the signer’s documented constraint; it cannot claim upload happened.
4. Credentials rotate or are revoked after signing. URL validity/revocation is
   provider-specific; core documents no universal revocation promise.
5. Key is replaced/deleted/versioned after GET URL creation. Result follows
   backend object/version semantics; sign option may need explicit version
   capability rather than assuming current object remains same.
6. An application asks signing for a prefix or arbitrary method. First capability
   supports one parsed key and fixed GET/PUT only; broader delegation needs ADR.
7. Client clock is skewed. Expiry is signer/backend semantics; returned error is
   bounded and URL/credential details remain secret.

### Invariants and acceptance evidence

- signed URL type has redacted `String`/no implicit formatting policy;
- no URL, query signature, credential, bucket or raw key in storage logs/OTel;
- fs adapter reliably declares capability unsupported;
- expiry/method/key/header binding fixtures run against selected S3 targets;
- audit/test examples show policy authorization before signing;
- URL issuance has no effect on object existence, write atomicity or metadata.

### First implementation slice

Defer until core Put/Open and S3 adapter are stable. Use a dedicated capability
interface rather than adding `Sign` to universal `Store`, and document returned
URL as sensitive `[]byte`/opaque value lifecycle where possible.

---

## S-10 — delete, version history and retention truthfulness

**Decision.** `Delete` means the backend applied the configured current-object
deletion operation. It does not mean bytes are irrecoverable, all versions are
gone, retention is satisfied, replicas purged or a signed URL is revoked. Version
restore/purge/governance are separate optional capabilities with explicit access
and provider semantics.

### Top-level declarative DX

```go
err := documents.Delete(ctx, key, storage.DeleteOptions{
    Mode: storage.CurrentObject,
})
```

### Happy use cases

1. An ordinary fs store removes visible current file after authorization; a
   later `Head` returns not found subject to filesystem/external race semantics.
2. An S3 versioned bucket applies a delete marker/current delete. Core reports
   delete completion without claiming historic versions are erased.
3. A domain writes a tombstone/audit record before/with delete according to its
   own transaction/retry contract; storage handles physical object action only.
4. A future version capability returns opaque version descriptors and an explicit
   restore/delete-version operation only after AWS/MinIO/R2 parity is proven.
5. Object-lock/retention refusal maps to bounded forbidden/precondition class,
   letting application distinguish policy/governance from missing key safely.
6. Repeated idempotent application cleanup uses documented Delete absent-object
   policy (`not_found` vs explicitly chosen idempotent success), never ambiguous.

### Edge use cases

1. Delete request times out after provider may have applied marker. Adapter
   returns uncertainty; caller checks `Head`/version state rather than assuming.
2. A delete target changes between authorization and operation. Conditional
   delete, if offered, needs opaque validator capability; ordinary delete does
   not promise it removed the originally inspected bytes.
3. Fs deletion succeeds but snapshots/backups retain data. Documentation avoids
   “secure erase” claims; regulated purge needs a distinct platform process.
4. S3 lifecycle later deletes old versions. Core does not use lifecycle state as
   synchronous Delete success or expose provider retention configuration loosely.
5. A legal hold/retention error contains object/version identifiers. Error
   projection suppresses raw provider response from public/telemetry paths.
6. Caller requests recursive delete on a prefix. First core refuses: it is a
   listing/cardinality/authorization operation, not one object deletion.
7. An object has a pre-signed URL active after delete. Provider may return
   absence/older version; application must not advertise universal revocation.

### Invariants and acceptance evidence

- docs use `Delete`/delete-marker/current-object wording, never generic erase;
- conformance distinguishes absent, current deletion, versioned marker and
  retention refusal for every declared backend mode;
- no recursive prefix deletion arrives through raw key APIs;
- raw version IDs/provider governance text stay absent from defaults;
- audit example records requested action/result separately from trace sampling.

### First implementation slice

Implement current-object delete with an explicit absent-object policy. Defer
version enumeration, purge, restore, lifecycle management and retention APIs
until their security/regulatory semantics have independent designs.

---

## S-11 — listing, prefixes and pagination are deferred capability work

**Decision.** List is not included in the first common store because it reveals
namespace inventory, requires a consistency/pagination contract, and differs
substantially between filesystem traversal and S3 prefix/delimiter listing. A
future list capability must be authorization-aware and cursor-bounded from day
one.

### Top-level declarative DX

```go
// Deliberately future API, not first-core behaviour.
page, err := lister.List(ctx, storage.ListOptions{
    Prefix: safePrefix,
    Limit: 100,
    Cursor: cursor,
})
```

### Happy use cases

1. An application uses its own database index to list user-visible documents,
   then calls `Head/Open` for individual authorized object keys.
2. A maintenance tool with separate admin authority uses a future bounded list
   capability over a configured namespace/prefix and opaque cursor.
3. S3 paginator continuation token remains adapter-owned opaque cursor; callers
   never depend on lexical key ordering or provider token format.
4. Fs traversal uses explicit symlink/permission/root policy and produces same
   portable page result only where ordering/visibility contract permits.
5. List response emits metadata only from an approved small `Info` subset, not
   object body, raw path/bucket/SDK object response or unbounded user metadata.

### Edge use cases

1. Prefix derives from tenant/customer input. It needs policy scope and must not
   become raw storage capability in a public API by default.
2. Namespace has millions of objects. Limit is mandatory/max-bounded; adapter
   never materializes all items before returning first page.
3. Objects change during iteration. Contract documents snapshot-free/weak
   consistency and duplicate/missing possibilities, or offers an expensive
   backend-specific snapshot capability separately.
4. Cursor is tampered, expired, from another namespace or too large. Adapter
   returns invalid cursor without backend/key disclosure and never tries to parse
   it as trusted storage key text.
5. S3 delimiter common-prefix pseudo-directories differ from fs directories.
   Core does not expose directory booleans unless a portable semantic exists.
6. A recursive cleanup tool uses list then delete. Race/authorization/retention
   proof belongs to a named maintenance workflow, not `DeleteAll(prefix)`.
7. An operator wants exact object count metric. It is a scan/costly eventual
   value; never emitted as per-namespace default metric.

### Invariants and acceptance evidence

- no `List` method ships on first `Store` interface;
- future proposal supplies visibility/ordering/consistency/cursor/authorization
  decision before code;
- pagination fuzz corpus rejects hostile tokens without panics or value leaks;
- adapter test proves bounded memory/network calls per page;
- inventory/prefix values are forbidden default telemetry attributes.

### First implementation slice

Write an ADR and a database-indexed application use case instead of code. Do
not let an S3 SDK’s convenient paginator set public contract semantics by
accident.

---

## S-12 — observability, audit and error correlation

**Decision.** Storage emits no telemetry by default. A future storage-OTel
decorator follows the OTel roadmap’s safe vocabulary; audit writes remain an
authorized durable domain concern. Both observe storage result, neither turns
the object store into an event/audit authority or records sensitive identifiers.

### Top-level declarative DX

```go
objects := storageotel.Decorate(documents, storageotel.Config{
    Telemetry: telemetry,
    StoreName: "documents",
})
```

### Happy use cases

1. `Put` emits one `vv.storage.put` logical span below command span with store
   family, bounded size bucket and outcome; S3 SDK HTTP spans remain optional.
2. Audit decorator records actor/action/domain object and storage result per
   audit roadmap, optionally correlating trace context but never copying bytes.
3. A storage error maps into service/public error contract and OTel records its
   bounded outcome/code without calling raw SDK `Error()` strings.
4. An fs and S3 conformance run yields same logical telemetry vocabulary for the
   core operation, despite different backend implementation details.
5. A multipart operation records one logical store span plus bounded lifecycle
   metric, not a span per part/object chunk by default.

### Edge use cases

1. Object key is personal data or an opaque support ID. It is absent from trace,
   metric, log correlation and default error serialization.
2. Bucket/endpoint includes customer/location/security boundary. It remains host
   configuration, absent from vv storage telemetry dimensions.
3. Audit wants original key/version for regulated evidence. Audit store may
   retain it under authorization; OTel never gains it by correlation.
4. Exporter is down or trace unsampled. Put/Open/Delete outcomes and audit
   atomicity stay exactly as without decorator.
5. Large batch cleanup invokes thousands of deletes. Decorator caps span/events
   and gives summary metric; it cannot allocate per-key telemetry exhaust.
6. Backend raw error contains signed URL/request ID. Only safe mapped condition
   reaches generic telemetry; application can preserve raw diagnostic elsewhere.

### Invariants and acceptance evidence

- storage core imports neither OTel nor audit packages;
- telemetry privacy corpus includes keys, paths, buckets, signed URLs, version
  identifiers, metadata and provider raw errors;
- decorator preserves reader/body and source ownership exactly;
- audit fixtures prove correctness when tracing disabled/unsampled;
- error mapping is one-way/bounded and does not guess provider code semantics.

### First implementation slice

Do not write the decorator until storage conformance suite and OTel O-13 bridge
vocabulary are accepted. Add trace fixture first, then instrumentation in a
separate satellite so fs-only/core consumers keep zero OTel dependency.

---

## S-13 — tenancy, authorization and backend topology

**Decision.** Storage keys and namespaces never decide tenant authorization by
themselves. The tenancy/policy layer resolves an authorized logical tenant/domain
scope before storage invocation; backend routing (one bucket/root, prefix, or
store per tenant) is a separately declared deployment topology with no identity
leak into the common Store API.

### Top-level declarative DX

```go
tenantDocuments, err := tenantstorage.ForTenant(ctx, documents, tenant)
// tenant resolution/policy occurs before caller receives the scoped store.
```

### Happy use cases

1. One shared S3 bucket uses an application-controlled opaque prefix after
   tenant policy resolution; callers receive only a scoped Store and cannot
   escape prefix with a crafted key.
2. A one-database/one-filesystem application uses tenant row policy in its
   domain database and a single storage namespace; object authorization remains
   checked on every domain operation before Open/Sign/Delete.
3. A database-per-tenant deployment chooses a per-tenant bucket/root/store at
   resolver bootstrap, while core storage operations remain identical and do
   not expose backend container identity.
4. A super-admin maintenance workflow receives an explicitly broader scoped
   store through its policy capability, rather than passing `../` or a raw S3
   prefix into ordinary user store methods.
5. Audit records hold the tenant/object subject under authorization; OTel sees
   only bounded tenant topology mode and storage outcome as defined elsewhere.

### Edge use cases

1. A tenant ID is embedded in raw key. Core permits only syntactically valid
   key; tenancy layer must still authorize scope and telemetry never exports it.
2. Resolver returns wrong prefix/bucket/root. Existing tenancy integration tests
   must fail; storage cannot certify that a scoped adapter was authorized.
3. Tenant is deleted or reassigned while an open body/signed URL exists. Domain
   lifecycle decides revocation/retention; storage makes no universal guarantee.
4. Database-per-tenant creates 100,000 backend clients. Resolver/pool lifecycle
   must cap resources; core Store does not cache unbounded tenants globally.
5. Prefix collision occurs due encoding/case normalization. Tenant mapper uses
   opaque validated mapping, not display names, and has collision tests.
6. A storage repair job crosses tenants. It has a named admin capability, audit
   event and bounded batch design; no `ListAllTenants` convenience API exists.
7. Tenant-specific encryption/retention is requested. This is provider/domain
   capability with key-management decision, not user metadata/free options.

### Invariants and acceptance evidence

- plain `Store` has no `TenantID` argument/metric attribute;
- scoped adapter adversarial tests show key cannot escape authorized root/prefix;
- topology test suite covers shared and per-tenant backend without changing
  common core conformance output;
- tenant identity/database/bucket/schema/connection details remain absent from
  errors, telemetry and default logs;
- disabling telemetry does not weaken authorization/routing tests.

### First implementation slice

Do not add tenancy adapter before the separate multitenancy roadmap defines
resolver, lifecycle and one-db/db-per-tenant contracts. Start with an example
showing domain policy plus opaque key mapping, not a root-level tenant field.

---

## S-14 — event sourcing, outbox and object lifecycle coordination

**Decision.** Object bytes are not event payloads and object storage is not the
event store. Domain events contain stable logical references/metadata approved
by domain contract; Postgres event/outbox transaction can coordinate intent but
cannot atomically commit a remote S3 object without staged/recovery protocol.

### Top-level declarative DX

```go
// Illustrative domain workflow:
stage := storage.Stage(...)
// persist object-intent/event/outbox in PostgreSQL
// promote/reconcile under explicit lifecycle state machine
```

### Happy use cases

1. A command stages document bytes, writes an event `document.upload_requested`
   with a logical document reference in PostgreSQL, and later promotes/reconciles
   object under explicit state machine.
2. Event payload records stable domain document ID and content metadata allowed
   by schema, not S3 URL, bucket, pre-signed URL, SDK response or raw key unless
   the event schema security review explicitly approves a logical key.
3. Outbox publisher emits event only after DB transaction commits; object upload
   state is retried/reconciled separately and observability shows causal links.
4. A projection wants bytes. It uses authorized storage Open by domain reference,
   not event payload/blob replay that loads huge objects into event handlers.
5. Audit captures domain lifecycle transitions (requested, staged, available,
   failed, deleted) with durable details; storage traces remain short-lived.

### Edge use cases

1. S3 promotion succeeds but PostgreSQL state update fails. Reconciler detects
   idempotent state using opaque staging/final references; no manual guessing by
   object prefix/list is required.
2. DB transaction commits but object upload fails. Event/version state makes
   failure/retry visible; consumer cannot assume event means bytes available.
3. Event replay re-executes historical upload transition. Handler must be pure
   or idempotent and must not reupload bytes just because historic event is read.
4. An event evolves from local fs reference to S3 storage. Event schema stores
   logical domain reference so backend migration doesn't require rewriting event
   payloads or binding replay to past filesystem paths.
5. Object retention conflicts with event-driven delete. Domain state can record
   deletion requested/blocked; storage error remains bounded and no false erase.
6. A large object checksum is included in event. Only explicitly bounded chosen
   digest is allowed after event schema review; never arbitrary source bytes.
7. An outbox task is delivered twice. Storage promotion/reconciliation must be
   idempotent by its own conditional protocol, not rely on exactly-once broker.

### Invariants and acceptance evidence

- no S3/fs SDK type, URL or credentials appear in event-store root contracts;
- crash matrix covers before/after stage, DB commit, event publish, promote,
  state transition and cleanup;
- event replay fixture proves it never implicitly retrieves/uploads object bytes;
- domain lifecycle is complete with OTel disabled and broker delivery duplicated;
- audit and storage retention semantics remain separately authoritative.

### First implementation slice

Write a concrete document-upload state machine in the event-sourcing roadmap
before public `Stage`. Treat remote object coordination as saga/reconciliation,
not an imaginary distributed transaction or a reason to put S3 calls in a DB tx.

---

## S-15 — capability negotiation, versioning and adapter evolution

**Decision.** `Capabilities` is a small, immutable, versioned declaration of
semantics the constructed store can actually satisfy. It is not a dynamic list
of provider marketing features. Applications request capability-specific
interfaces and fail at bootstrap where that is safer than late production error.

### Top-level declarative DX

```go
signer, ok := storage.AsSigner(documents)
if !ok { return errors.New("document store cannot sign uploads") }
```

### Happy use cases

1. An fs store reports core streaming/read/write/delete capability and no signed
   URLs/versions; app presents a proxied download path.
2. An S3 store reports signer only after client/bucket/signer configuration is
   validated; application fails startup if direct-browser upload is required.
3. An adapter version adds a new optional capability. Existing core consumers
   keep working without branch changes or altered Put/Open semantics.
4. A backend supports native versioning but core marks it experimental until its
   version/delete/restore conformance fixtures pass against declared providers.
5. Feature flags select a backend at deploy time; each adapter's capabilities
   are logged/audited safely by names, not endpoint/bucket/credential details.

### Edge use cases

1. Provider configuration changes after construction (bucket versioning disabled
   or policy altered). Adapter cannot guarantee immutable remote capabilities;
   operation returns bounded unsupported/forbidden and health check detects drift.
2. An app casts concrete fs/S3 type to access internals. Docs mark that outside
   compatibility contract; only capability interfaces are stable extension seam.
3. Same S3 provider has partial support under different region/endpoint. Adapter
   capability applies to configured instance, with real integration evidence.
4. Adding a bool to `Capabilities` accidentally promises semantics no test
   defines. Every capability requires operation/error/consistency acceptance
   table and conformance fixture before publication.
5. An adapter returns changing capability per request based on transient error.
   Immutable construction capability stays declarative; availability is operation
   outcome/health state, not mutation of public feature identity.
6. Consumer needs provider-only retention tag. It belongs to a separately named
   provider extension/satellite, never vague `Options.Raw` passthrough.

### Invariants and acceptance evidence

- capabilities have finite names/descriptions and documented stability class;
- no raw SDK object/options map crosses core capability boundary;
- required-capability bootstrap test and late remote-drift test are separate;
- capability additions receive semantic version/release note review;
- test suite proves unsupported path performs no partial core side effect.

### First implementation slice

Implement only `Core`, `ConditionalCreate` if proven, and test-only capability
query. Delay a generic `Capabilities` flags expansion until each extension has a
consumer use case and cross-backend conformance table.

---

## S-16 — conformance suite and target matrix

**Decision.** Store correctness is demonstrated by one backend-neutral suite
that runs against secure fs, a deterministic fake, MinIO and declared cloud
targets. Provider integration tests are not optional “acceptance” tests after
the adapter ships; they are evidence for each compatibility claim.

### Top-level declarative DX

```go
storagecontract.Run(t, storagecontract.Subject{
    NewStore: newConfiguredStore,
    Capabilities: expected,
})
```

### Happy use cases

1. Every adapter passes parsed-key, Put/Open/Head/Delete, metadata, source/body
   ownership, conditional-create and error-projection cases it claims.
2. Fs secure suite additionally covers permission, symlink/race and same-root
   staging; fake deliberately cannot be used as proof for these properties.
3. MinIO test runs in isolated ephemeral deployment with explicit credentials/
   bucket and validates S3 request semantics in CI/integration lane.
4. AWS and R2 target suites run under isolated test account/project with
   lifecycle cleanup, cost/credentials policy and recorded compatibility version.
5. A new adapter must declare unsupported optional scenarios rather than skip
   them silently; skip reasons are part of conformance report.

### Edge use cases

1. Cloud integration credentials are absent. CI marks target contract unverified
   for that change; it does not substitute fake success as AWS/R2 compatibility.
2. Test cleanup delete fails due version/retention/network. Harness records
   bounded cleanup artifact and uses account lifecycle policy, never broad bucket
   deletion based on unresolved prefix.
3. Parallel tests share bucket. Harness creates validated randomized test prefix
   and proves operations cannot escape it; test prefixes do not enter production
   telemetry/schema dimensions.
4. Provider changes behaviour. Recorded target version/date and nightly/periodic
   contract lane reveal drift; maintainers update capability docs deliberately.
5. Network fault injection drops completion response. Conformance asserts
   uncertainty rather than a provider-specific fabricated success.
6. Race test cannot be made deterministic on remote S3. Harness uses controlled
   barriers/conditional requests and states limit; no bogus atomicity claim.

### Invariants and acceptance evidence

- core tests run without network; provider claims cite real-target suite;
- contract suite has negative/security/resource-leak cases, not just round trips;
- all test credentials/endpoints/keys are explicit secret-managed setup;
- cleanup scope is validated non-broad before destructive operation;
- compatibility report lists target, date, SDK/version, capability results and
  known divergence/deferral.

### First implementation slice

Build test fixture architecture before selecting S3 SDK. A successful `Put` to
one developer's bucket is not a sufficient acceptance criterion for a public
storage satellite.

---

## S-17 — credentials, encryption and trust-boundary policy

**Decision.** Storage accepts a caller-created backend client/credential source;
it neither reads ambient secrets nor owns key rotation. Transport encryption,
server-side encryption and client-side encryption have materially different
failure/recovery/key-management semantics and cannot be one `Encrypt: true`
option in the common core.

### Top-level declarative DX

```go
client := newApplicationOwnedS3Client(credentials, transport)
store, err := storages3.New(storages3.Config{Client: client, Bucket: bucket})
```

### Happy use cases

1. Application injects short-lived credential provider/client; adapter makes
   ordinary operations and maps an authorization/expiry failure safely.
2. Production requires TLS endpoint verification; insecure local MinIO is an
   explicit development configuration outside storage core defaults.
3. Backend/server-side encryption is configured by infrastructure/client policy
   and its required headers/capabilities are tested in that adapter profile.
4. A domain needing end-to-end client encryption uses a separate cryptographic
   layer that owns keys, algorithm/version, streaming framing and rotation; its
   encrypted bytes are ordinary storage content.
5. Audit records credential-policy/config changes through infrastructure controls
   rather than embedding secret/provider parameters in object metadata.

### Edge use cases

1. Access key/secret appears in endpoint error or SDK debug logger. Adapter
   default error projection and test logging redact/suppress raw request dumps.
2. Credential renewal occurs after partial multipart state. Adapter treats it as
   uncertain operation, follows explicit abort/reconcile, never replays bytes
   automatically assuming a new token proves prior result absent.
3. Server-side encryption key policy rejects one object. Core reports bounded
   forbidden/precondition result; it does not fall back to unencrypted upload.
4. Client-side encryption key is deleted. Storage `Open` can still return bytes;
   crypto/domain layer owns decrypt failure and retention story.
5. A staging object is encrypted under a temporary key then promoted. Key
   lifecycle/re-encryption requires explicit domain workflow, not storage rename.
6. An operator requests raw SDK debug tracing for incident. It is time-bounded,
   access-controlled host setup and explicitly excluded from vv telemetry/audit
   defaults because it can expose authorization headers and signed URLs.

### Invariants and acceptance evidence

- core has no credential/env/region/profile configuration fields;
- redaction corpus contains access token, secret key, signed URL and TLS error;
- no automatic fallback weakens TLS/encryption configuration;
- provider client lifecycle/close/shutdown remains application-owned;
- encryption feature documentation separates transport, server-side and client-
  side guarantees with no generic at-rest compliance claim.

### First implementation slice

Require caller-injected client for S3 adapter. Do not provide credential-chain
helpers until an explicit satellite decision shows they are a single consumer
choice and their security/update lifecycle can be responsibly supported.

---

## S-18 — migration, backup, release and operational ownership

**Decision.** Moving objects between fs, MinIO, AWS or R2 is an application/
platform migration with inventory, integrity, authorization and rollback plan.
The storage core supports portable read/write semantics; it does not promise an
online backend switch, bulk copy utility or backup/restore system by accident.

### Top-level declarative DX

```text
release vN: write new objects to backend B;
read B then authorized fallback A;
verify inventory/integrity; cut over; retain A by explicit policy.
```

### Happy use cases

1. App introduces a migration resolver that reads logical document reference from
   domain DB, tries new backend then old backend under explicit authorization,
   and records progress in durable migration state.
2. A copy worker streams object A to a `CreateOnly`/staged key in B, verifies
   approved integrity/metadata, then atomically updates domain routing state.
3. Rollback leaves old backend bytes intact until retention/cutover evidence is
   complete; no immediate `DeleteAll` is performed after first successful copy.
4. New storage satellite release changes metadata/key grammar/capability. It
   publishes compatibility matrix and migration tool/test fixtures before flip.
5. Backups are handled by filesystem snapshot/object versioning/provider policy
   plus application database backup coordination; docs identify recovery owner.
6. Monitoring tracks bounded migration lifecycle totals and errors, while audit
   holds per-object identity/progress under proper access rather than metrics.

### Edge use cases

1. Old backend object differs/corrupts while copying. Migration marks durable
   failure, retains evidence and never silently overwrites target or calls it
   success because byte count happens to match.
2. Object key grammar is tightened in a new release. Existing unsafe legacy keys
   need read-only legacy mapping/migration plan; new parser cannot silently map
   them to a different logical object.
3. A versioned S3 bucket contains delete markers/old versions. Migration scope
   explicitly decides current-only/history, retention/legal hold and restore;
   it never assumes simple list provides complete compliance inventory.
4. A database transaction changes object routing but worker crashes. Durable
   state machine/reconciler resumes; users never receive a random backend URL.
5. Encryption strategy changes. Migration has decrypt/re-encrypt/rekey protocol
   owned by crypto domain; storage copy has no generic “reencrypt” promise.
6. Release rollback reintroduces old adapter that cannot parse new metadata.
   Compatibility fixture detects it before deployment, or release keeps old
   readable representation until rollback window passes.
7. Backup restore replays object bytes but not database/audit mapping. Runbook
   treats cross-system consistency as explicit restore procedure, not automatic.

### Invariants and acceptance evidence

- no core method named `Migrate`, `Backup`, `Restore` or `CopyAll` ships first;
- migration design has durable state, idempotency, audit, authorization, rollback
  and retention sections before operational run;
- key/metadata/schema release diffs have old/new/read/write compatibility tests;
- destructive cleanup targets are resolved/validated and rate/batch bounded;
- runbook distinguishes object data, database references, event/audit history
  and provider configuration recovery responsibilities.

### First implementation slice

Provide a documented reference migration state machine and a test-only dual
backend fixture. Defer a general migrator CLI: deployment identity, credentials,
retention and authorization are product-specific external decisions.

---

## Storage completion gates

The first storage release is complete only when:

1. an ADR fixes key grammar, namespace mapping, write modes, error projection,
   source/body ownership and documented fs durability mode;
2. core and fs-only user have no cloud SDK, exporter, global config or ambient
   credential dependency;
3. every visible write is staged/placed so partial source/cancel failure cannot
   appear as a successful final object;
4. `CreateOnly` race semantics pass concurrent fs and selected S3 tests, or
   adapter explicitly reports capability unsupported;
5. all metadata/key/URL/error privacy corpora pass without value leaks;
6. secure fs root/symlink/race tests support every advertised platform/mode;
7. one selected S3 SDK adapter passes real MinIO and documented cloud target
   tests for every stated common capability;
8. multipart abort/uncertain completion/retry behaviour is documented and
   tested, with no invisible write retry;
9. storage health/telemetry/audit bridges are optional and removing them leaves
   source/body, error, authorization and durable state semantics unchanged;
10. signed URLs, versioning, List, retention and migration remain deferred unless
    their corresponding capability-specific test/decision gates are complete.

---

## Contract scenario catalogue

Each completed adapter runs every applicable scenario below. A scenario checks
functional result, resource ownership, safe error class, backend-neutral
metadata and absence of sensitive values. The deterministic fake can exercise
core logic; only secure fs and real target suites can prove their own semantics.

### SC-01 — construction isolates backend choice

**DX.** A consumer constructs fs or S3 adapter explicitly and passes it to one
common core constructor.

**Setup.** Prepare a valid temporary fs root and a valid injected S3 fake client.

**Action.** Construct two stores with same namespace.

**Happy assertion.** Both expose identical core operation surface.

**Capability assertion.** S3-only signer is absent from fs store.

**Privacy assertion.** Root/bucket/endpoint values do not appear in `Info`.

**Edge setup.** Give S3 adapter nil client and fs adapter relative unsafe root.

**Edge assertion.** Construction refuses both without backend writes.

**Control assertion.** No process-global config/client state changes.

### SC-02 — key parser accepts one portable happy path

**DX.** Caller calls `ParseKey` before every core operation with a raw input.

**Setup.** Use `documents/2026/report.pdf` as a representative logical key.

**Action.** Parse then Put, Head, Open and Delete through each adapter fake.

**Happy assertion.** Every operation resolves the same opaque logical identity.

**Mapping assertion.** Fs/S3 physical location stays adapter-private.

**Privacy assertion.** Key never becomes default error/telemetry/log attribute.

**Edge setup.** Repeat with an allowed opaque generated ID segment.

**Edge assertion.** Parser does not decode/application-interpret the ID.

**Control assertion.** String mapping cannot differ across operations.

### SC-03 — traversal is refused before backend contact

**DX.** Parser returns typed invalid-key error, not adapter-specific path error.

**Setup.** Table drive `../x`, `a/../../x`, `/x`, `a//b`, `a/./b` and empty key.

**Action.** Attempt ParseKey and all convenience operation entry points.

**Happy assertion.** Valid control key continues to work.

**Error assertion.** Each malicious form is invalid without raw value echo.

**Resource assertion.** Source reader is not read and no staging file/request starts.

**Edge setup.** Use an extremely long traversal string.

**Edge assertion.** Length rejection happens before expensive split/join work.

**Control assertion.** No backend fake method call is observed.

### SC-04 — platform separator ambiguity is not portable

**DX.** Key grammar documentation says how backslash and reserved forms behave.

**Setup.** Feed `a\\b`, `CON`, trailing-space and case-collision candidates.

**Action.** Parse under portable fs mode and S3 conformance mode.

**Happy assertion.** Safe canonical key remains unchanged.

**Error assertion.** Unsafe forms are refused consistently.

**Security assertion.** Linux acceptance cannot create Windows escape later.

**Edge setup.** Use Unicode confusable/control character sequences.

**Edge assertion.** Chosen normalization/UTF-8 rule is deterministic and bounded.

**Control assertion.** Provider permissiveness never expands core grammar.

### SC-05 — stream source is never closed by Put

**DX.** Put accepts ordinary `io.Reader`, not a storage-owned body wrapper.

**Setup.** Reader fake records reads and an optional caller-owned close action.

**Action.** Put a successful small object.

**Happy assertion.** Bytes and returned Info match expected portable data.

**Ownership assertion.** Store does not call optional source Close.

**Memory assertion.** Fake confirms incremental reads rather than one ReadAll.

**Edge setup.** Reader returns error after several chunks.

**Edge assertion.** No final success Info; staging/multipart cleanup starts.

**Control assertion.** Caller can still close its source normally afterwards.

### SC-06 — returned body is caller-owned and closable

**DX.** Open returns one `io.ReadCloser` with a documented Close requirement.

**Setup.** Put known bytes then Open through fs/S3 fake body tracking.

**Action.** Read a prefix and close body.

**Happy assertion.** Prefix is correct and Info is available as documented.

**Ownership assertion.** Close releases underlying descriptor/response exactly once.

**Privacy assertion.** Body/error does not expose backend physical path/URL.

**Edge setup.** Close twice and read after close according to Go contract.

**Edge assertion.** No panic/leak/retry; documented error/no-op semantics apply.

**Control assertion.** Full read/close also leaves no resource leak under loop.

### SC-07 — source size mismatch does not fabricate Info

**DX.** PutOptions makes declared size optional and validates mode clearly.

**Setup.** Declare 10 bytes, source emits 9; then declare 9, source emits 10.

**Action.** Execute Put under both fs and multipart-capable fake adapters.

**Happy assertion.** Matching-size control completes with correct Info size.

**Error assertion.** Mismatch returns documented source/options condition.

**Visibility assertion.** No partial final key becomes visible after mismatch.

**Edge setup.** Mismatch happens only after multipart threshold/parts accepted.

**Edge assertion.** Adapter attempts bounded abort and reports primary error safely.

**Control assertion.** Caller reader ownership is still unchanged.

### SC-08 — cancellation does not pretend absence

**DX.** Caller passes normal context; no special storage cancellation flag exists.

**Setup.** Blocking reader/backend fake exposes barriers between chunks/requests.

**Action.** Cancel context during Put and separately during Open.

**Happy assertion.** Uncancelled control operations complete normally.

**Error assertion.** Caller sees cancellation/deadline class as specified.

**Visibility assertion.** Result does not state final object absent unless proven.

**Edge setup.** Backend completes just before cancellation signal arrives.

**Edge assertion.** Adapter returns observed/uncertain result, never guessed state.

**Control assertion.** No arbitrary retry occurs after cancellation.

### SC-09 — CreateOnly has one winner on filesystem

**DX.** One named write mode expresses create-if-absent; no boolean ambiguity.

**Setup.** Synchronize two fs store writers for same parsed key and distinct data.

**Action.** Release both attempts concurrently.

**Happy assertion.** Exactly one returns success and final bytes equal its source.

**Error assertion.** Other gets AlreadyExists/precondition class, not overwrite.

**Integrity assertion.** Final object contains neither partial nor mixed bytes.

**Edge setup.** Repeat across processes where supported, not goroutines only.

**Edge assertion.** Same single-winner invariant holds or mode is unsupported.

**Control assertion.** Unconditional mode is not accidentally enabled.

### SC-10 — CreateOnly remote uncertainty is preserved

**DX.** S3 adapter exposes uncertainty as a bounded typed result.

**Setup.** Fake remote accepts conditional put but drops completion response.

**Action.** Put with CreateOnly and inspect returned result.

**Happy assertion.** Normal response control returns success with opaque Info.

**Error assertion.** Dropped response is temporary/uncertain, not `ok` or exists.

**Privacy assertion.** Request ID/URL/raw protocol error stays hidden.

**Edge setup.** Follow with Head and see object exists/nonexists in two variants.

**Edge assertion.** Reconciliation is caller/domain decision, not hidden retry.

**Control assertion.** Source is not replayed automatically.

### SC-11 — contradictory write options are refused locally

**DX.** Options validator produces field-level safe configuration error.

**Setup.** Request CreateOnly with ReplaceIfMatch and invalid empty validator.

**Action.** Invoke Put using a reader fake that tracks first read.

**Happy assertion.** A valid single mode control reaches store successfully.

**Error assertion.** Contradiction is returned before backend/source work.

**Resource assertion.** Reader read count remains zero; no staging exists.

**Edge setup.** Combine future version/delete conditional flags deliberately.

**Edge assertion.** Validator rejects combinations deterministically.

**Control assertion.** Error text contains mode class, not user key/metadata.

### SC-12 — metadata portable round trip is bounded

**DX.** Metadata type validates known safe keys/values before Put.

**Setup.** Use content type and `classification=public-thumbnail` metadata.

**Action.** Put, Head and Open using fs and S3 fake adapters.

**Happy assertion.** Portable metadata and content type return equivalently.

**Ownership assertion.** Mutating returned map cannot change subsequent Head.

**Privacy assertion.** Backend system headers remain out of user metadata map.

**Edge setup.** Supply over-limit map/value and case-colliding keys.

**Edge assertion.** Validator refuses before streaming/request serialization.

**Control assertion.** Safe metadata does not acquire provider-specific casing.

### SC-13 — metadata cannot become a secret bag

**DX.** Reserved/sensitive metadata keys are documented and executable rules.

**Setup.** Put metadata containing token, authorization, cookie, email and tenant.

**Action.** Validate options and attempt Put.

**Happy assertion.** Approved classification control is accepted.

**Error assertion.** Sensitive/reserved form is refused safely if core owns rule.

**Telemetry assertion.** Values never appear even on validation error path.

**Edge setup.** Hide a sentinel in nested JSON-looking metadata value.

**Edge assertion.** Total-size/scanner tests prevent passthrough leakage.

**Control assertion.** Domain may store sensitive mapping in protected DB/audit.

### SC-14 — multipart source failure cleans owned state only

**DX.** Large input uses ordinary Put; multipart choice is adapter internal.

**Setup.** Remote fake accepts two parts then reader emits sentinel error.

**Action.** Execute Put over multipart threshold.

**Happy assertion.** Large successful control completes one visible final object.

**Error assertion.** Caller receives source failure as primary result.

**Cleanup assertion.** Adapter aborts known upload/staging identity once.

**Edge setup.** Abort itself fails due transient remote error.

**Edge assertion.** Diagnostic remains bounded; no final object is claimed absent.

**Control assertion.** Cleanup cannot delete another process final object/key.

### SC-15 — filesystem staging never exposes partial final bytes

**DX.** Fs adapter staging mechanism is internal; caller sees ordinary Put.

**Setup.** Slow reader writes known chunks while concurrent reader polls final key.

**Action.** Fail source halfway, then repeat with successful completion.

**Happy assertion.** Successful case becomes visible only with full expected bytes.

**Error assertion.** Failed case has no final object/success Info.

**Security assertion.** Staging filename cannot be parsed as visible user key.

**Edge setup.** Process crashes before placement; invoke maintenance cleanup fake.

**Edge assertion.** Cleanup targets owned stale stage only and is idempotent.

**Control assertion.** Existing unrelated object under root is untouched.

### SC-16 — filesystem symlink swap cannot escape root

**DX.** Fs configuration documents platform/root permission prerequisites.

**Setup.** Create valid root then race attacker symlink replacement during Put/Open.

**Action.** Exercise paths at each resolve/open/rename boundary.

**Happy assertion.** Normal path control remains readable/writable under root.

**Security assertion.** No test operation reads/writes sentinel file outside root.

**Error assertion.** Unsafe/raced condition returns bounded refusal/internal class.

**Edge setup.** Replace root itself or insert nested symlink after validation.

**Edge assertion.** Adapter's advertised secure mode contains/refuses operation.

**Control assertion.** Test never needs broad destructive cleanup outside root.

### SC-17 — fs root replacement is contained

**DX.** Construction records a safe root handle/validation strategy.

**Setup.** Replace configured root directory after store construction in a test.

**Action.** Put and Open a valid key through the already-created adapter.

**Happy assertion.** Unchanged-root control retains normal behavior.

**Security assertion.** Operation never follows replacement to attacker location.

**Edge assertion.** Adapter refuses or follows documented pinned-root semantics.

**Control assertion.** No outside-root sentinel is read or written.

### SC-18 — fs permission failure preserves error class

**DX.** File and directory modes are explicit configuration, not process magic.

**Setup.** Create root/parent with denied read/write permission under test user.

**Action.** Put, Head, Open and Delete a valid parsed key.

**Happy assertion.** Permitted control root completes normally.

**Error assertion.** Denial maps to forbidden/unavailable/internal policy class.

**Privacy assertion.** Raw filesystem path and OS details stay out of public error.

**Control assertion.** No staging descriptor remains after failure.

### SC-19 — fs disk-full behavior has no false success

**DX.** Store returns typed storage error rather than leaking `ENOSPC` text.

**Setup.** Use quota/fault-injection filesystem that fails during source copy.

**Action.** Put a multi-chunk object through staging path.

**Happy assertion.** Capacity control case returns final Info normally.

**Error assertion.** Full-disk result is bounded and source error is preserved.

**Visibility assertion.** Final key is absent or old value semantics are explicit.

**Control assertion.** Staging cleanup is attempted only under configured root.

### SC-20 — fs file and directory durability modes are distinct

**DX.** Durability configuration names visibility, file-sync and directory-sync.

**Setup.** Fake fs records file sync, rename and directory sync calls/failures.

**Action.** Put a successful object under each configured durability mode.

**Happy assertion.** Calls match declared mode in deterministic order.

**Error assertion.** Directory-sync failure is not relabelled as ordinary success.

**Documentation assertion.** API makes no replication or backup guarantee.

**Control assertion.** Default mode is conservative and documented.

### SC-21 — Open has no implicit range semantics

**DX.** Core Open API cannot accidentally accept an unbounded range option.

**Setup.** Put a known binary object and request ordinary Open.

**Action.** Read body in partial and full variants.

**Happy assertion.** Both read patterns follow normal stream ownership contract.

**Edge assertion.** Consumer attempting seek/range uses no unsupported cast/API.

**Compatibility assertion.** Fs seekability cannot leak as a core promise.

**Control assertion.** Later range capability can be added without redefining Open.

### SC-22 — Head after concurrent replacement has documented meaning

**DX.** Conditional options are named rather than implied by Head result.

**Setup.** Read Head for object, replace it concurrently, then Open/Delete.

**Action.** Perform ordinary non-conditional operation.

**Happy assertion.** Operation reports current backend-observed result.

**Edge assertion.** It does not claim original Head object was acted upon.

**Capability assertion.** Validator conditional path is required for stronger rule.

**Control assertion.** No hidden reread/lock turns ordinary call into CAS.

### SC-23 — opaque validator is never a checksum promise

**DX.** Info labels validator/ETag as opaque backend value.

**Setup.** S3 fake supplies multipart-style non-content-hash ETag.

**Action.** Put then Head/Open and inspect Info representation.

**Happy assertion.** Caller can round-trip validator only through capability API.

**Integrity assertion.** Store never reports it as SHA/MD5/content checksum.

**Edge assertion.** Checksum feature requests explicit algorithm/digest option.

**Control assertion.** Fs adapter does not synthesize a misleading ETag.

### SC-24 — conditional replacement cannot use unsafe Head-then-Put

**DX.** ReplaceIfMatch capability reports availability at construction.

**Setup.** Backend fake offers only separate Head and unconditional Put calls.

**Action.** Request replacement with a current opaque validator.

**Happy assertion.** Strong conditional backend control succeeds once.

**Edge assertion.** Weak backend returns Unsupported, not simulated success.

**Race assertion.** Concurrent replacement cannot bypass condition through gap.

**Control assertion.** No source stream starts for unsupported mode.

### SC-25 — Delete absent-object policy is explicit

**DX.** DeleteOptions/policy names absent behavior at bootstrap or call site.

**Setup.** Use a parsed key that does not exist in fs and S3 fake stores.

**Action.** Delete under each supported absent-object policy.

**Happy assertion.** Idempotent mode returns documented success result.

**Error assertion.** Strict mode returns not-found without raw key value.

**Edge assertion.** Repeated delete has same deterministic behavior.

**Control assertion.** No prefix/list cleanup is attempted.

### SC-26 — versioned delete does not claim purge

**DX.** Delete mode explicitly says current object/delete marker where applicable.

**Setup.** Version-capable S3 fake has two historic versions for a key.

**Action.** Delete current object through core Delete.

**Happy assertion.** Current read behavior changes according to provider semantics.

**Privacy assertion.** Version identifiers stay out of default response/telemetry.

**Edge assertion.** Historic bytes remain recoverable where provider retains them.

**Control assertion.** API/documentation does not call operation secure erase.

### SC-27 — retention lock maps safely

**DX.** Store error taxonomy exposes retention/governance-safe condition class.

**Setup.** Backend fake refuses delete/replace due object lock/legal retention.

**Action.** Execute Delete and Replace modes.

**Happy assertion.** Unlocked control object follows ordinary mode behavior.

**Error assertion.** Locked result maps to forbidden/precondition as ADR decides.

**Privacy assertion.** Raw retention rule/version/key text stays hidden.

**Control assertion.** Adapter never retries or weakens governance header.

### SC-28 — signed GET has only one key and bounded expiry

**DX.** Signer accepts parsed Key, fixed method and validated duration.

**Setup.** Configured S3 signer and one authorized application key.

**Action.** Request ten-minute signed GET.

**Happy assertion.** Returned sensitive capability is accepted by target fake.

**Binding assertion.** Method/key/expiry follow declared sign request.

**Edge assertion.** Over-limit duration/prefix/wrong method is refused locally.

**Control assertion.** Fs signer capability remains unavailable.

### SC-29 — signed PUT does not prove upload

**DX.** SignPut result type/documentation calls it a capability, not an upload.

**Setup.** Request signed PUT then deliberately never invoke remote URL.

**Action.** Check Head before/after expiry in target fake.

**Happy assertion.** Existing normal Put control creates object and Info.

**Edge assertion.** Signing alone creates no object/revision/audit mutation.

**Telemetry assertion.** URL/signature/key never reach storage OTel attributes.

**Control assertion.** Application audits issuance separately if it needs evidence.

### SC-30 — signed URL formatting is redacted

**DX.** Signed capability has no unsafe default String/logger representation.

**Setup.** Produce URL containing unique credential/query sentinels.

**Action.** Format error, debug struct, trace and log helper test outputs.

**Happy assertion.** Authorized caller can use explicit raw value hand-off API.

**Privacy assertion.** Default formatting contains no sentinel/url query values.

**Edge assertion.** Wrapped signer error also hides provider request URL.

**Control assertion.** Safe operation/outcome metric remains available.

### SC-31 — S3 endpoint is caller-owned

**DX.** `storages3.Config` accepts injected client rather than endpoint secret bag.

**Setup.** Create target clients for AWS-style, MinIO-style and R2-style endpoint.

**Action.** Construct adapter and perform identical core Put/Open fake tests.

**Happy assertion.** Logical key/Info/error behavior stays common.

**Isolation assertion.** Core package does not import endpoint/credential config.

**Edge assertion.** Malformed/insecure endpoint fails in host client setup safely.

**Control assertion.** Endpoint/bucket are absent from default diagnostics.

### SC-32 — MinIO compatibility is evidence, not a label

**DX.** Target report names MinIO version and tested capability set.

**Setup.** Run contract suite against isolated real/fixture MinIO deployment.

**Action.** Execute common streaming, conditions, metadata and delete cases.

**Happy assertion.** Passing cells are recorded with date/SDK/target version.

**Edge assertion.** Unsupported/divergent behavior is explicit in capability report.

**Regression assertion.** Future adapter change reruns real-target suite.

**Control assertion.** Fake-only test cannot mark MinIO support complete.

### SC-33 — AWS compatibility is evidence, not an SDK import

**DX.** Cloud test setup passes explicit account/bucket/credential fixture config.

**Setup.** Isolated AWS S3 integration target with lifecycle cleanup policy.

**Action.** Run declared S3 adapter conformance cells.

**Happy assertion.** Results become versioned target evidence in release record.

**Edge assertion.** Missing credentials marks cloud cells unverified, not passed.

**Safety assertion.** Cleanup validates exact test prefix before destructive calls.

**Control assertion.** AWS SDK stays outside storage core/fs dependency graph.

### SC-34 — R2 compatibility has its own recorded subset

**DX.** R2 target profile is selected explicitly by host test configuration.

**Setup.** Isolated R2-compatible target and same parsed-key scenario corpus.

**Action.** Run core and optional capability cases.

**Happy assertion.** Documented supported cells pass with date/provider version.

**Edge assertion.** Any R2 divergence is capability-specific, never hidden.

**Privacy assertion.** Account/endpoint/bucket credentials remain test secrets.

**Control assertion.** “S3 compatible” does not imply untested retention/signing.

### SC-35 — provider raw errors cannot cross public boundary

**DX.** Adapter exposes typed error projection and controlled diagnostic unwrap.

**Setup.** Backend emits raw XML/HTTP errors containing URL, request ID and key.

**Action.** Invoke Put/Open/Delete/Sign failure paths.

**Happy assertion.** Caller receives documented condition/retry classification.

**Privacy assertion.** Public error, trace, metrics and default logs omit sentinels.

**Edge assertion.** Authorized application diagnostic may inspect wrapped cause only
under its own log/redaction policy.

**Control assertion.** Unknown provider error does not get falsely classified.

### SC-36 — storage telemetry is optional and logical

**DX.** Consumer decorates Store explicitly with a separately constructed OTel bridge.

**Setup.** Run Put with no-op, sampled and failing exporter provider fixtures.

**Action.** Compare returned Info/error and source/body ownership.

**Happy assertion.** Sampled trace has one `vv.storage.put` logical operation.

**Privacy assertion.** Key/path/bucket/metadata/version/URL are absent.

**Edge assertion.** Exporter failure/sampling changes no storage behavior.

**Control assertion.** Core/fs package has zero OTel dependencies.

### SC-37 — storage audit is durable and separate

**DX.** Audit decorator accepts declared logical subject/action policy.

**Setup.** Authorized domain upload writes object and audit action in test flow.

**Action.** Repeat with audit disabled, failure and unsampled trace.

**Happy assertion.** Audit contains declared subject/action/result only.

**Privacy assertion.** Bytes, signed URL and provider raw response are absent.

**Edge assertion.** Audit failure follows explicit domain transaction/saga policy.

**Control assertion.** Trace retention/sampling never determines audit completeness.

### SC-38 — staged promote has a terminal state machine

**DX.** Stage handle API exposes only valid `Promote` and `Abort` transitions.

**Setup.** Create stage, promote once, then attempt repeat promote/abort calls.

**Action.** Exercise every terminal/nonterminal transition under fake backend.

**Happy assertion.** One valid promotion yields one final Info.

**Edge assertion.** Repeated/contradictory transitions return defined terminal state.

**Cleanup assertion.** No terminal call deletes final object accidentally.

**Control assertion.** State handle/token stays opaque and unguessable.

### SC-39 — stage crash before database intent is recoverable

**DX.** Domain workflow records stage ownership/lifetime separately from raw key.

**Setup.** Stage bytes then crash before domain transaction persists intent.

**Action.** Run maintenance scanner with owned-stage registry/fake time.

**Happy assertion.** Valid persisted-stage control survives configured window.

**Edge assertion.** Unreferenced stale owned stage is cleaned under exact policy.

**Safety assertion.** Arbitrary prefix/object cannot be deleted by scanner.

**Control assertion.** Final visible key never appears from abandoned stage.

### SC-40 — stage crash after database intent is reconcilable

**DX.** Domain state names staged/pending/available/failed lifecycle explicitly.

**Setup.** Persist intent/outbox state then crash before promotion.

**Action.** Resume reconciliation worker with duplicate delivery simulation.

**Happy assertion.** Worker promotes or reports bounded retryable lifecycle state.

**Edge assertion.** Duplicate reconciler cannot produce two final visible objects.

**Audit assertion.** Lifecycle action is durable/audited where configured.

**Control assertion.** No invisible cross-system transaction is assumed.

### SC-41 — promotion final collision is conditional

**DX.** Promote accepts a final key and explicit create/replace condition mode.

**Setup.** Stage bytes and concurrently create final key via another writer.

**Action.** Promote under CreateOnly condition.

**Happy assertion.** No-collision control makes stage final once.

**Edge assertion.** Collision returns already-exists/precondition, preserves final.

**Cleanup assertion.** Stage remains/recovery state follows documented policy.

**Control assertion.** No destructive replacement occurs by default.

### SC-42 — S3 promotion copy and delete are not atomic rename

**DX.** Public API calls operation Promote rather than portable Rename.

**Setup.** Fake S3 copy succeeds then staged-delete fails or process crashes.

**Action.** Promote and inspect final/stage/reconciliation state.

**Happy assertion.** Final copy completion is reported according to contract.

**Edge assertion.** Cleanup failure is visible and never falsified as atomic move.

**Recovery assertion.** Reconciliation can identify owned residual stage safely.

**Control assertion.** Fs atomic rename semantics do not redefine S3 behavior.

### SC-43 — direct Put and Stage have distinct completion claims

**DX.** Application chooses direct Put or Stage explicitly in API/type surface.

**Setup.** Run equivalent source through direct visible key and staged workflow.

**Action.** Observe Info/lifecycle visibility before domain commit.

**Happy assertion.** Direct Put creates visible object at documented point.

**Edge assertion.** Stage does not make final key readable before Promote.

**Documentation assertion.** Neither call claims cross-DB atomicity.

**Control assertion.** No hidden staging burden for simple direct Put consumer.

### SC-44 — list is absent from first core interface

**DX.** External package cannot call `Store.List` because core does not expose it.

**Setup.** Compile consumer fixture needing document inventory.

**Action.** Attempt generic store list and then use domain DB index control.

**Happy assertion.** DB-indexed authorized list resolves individual keys safely.

**Edge assertion.** Generic prefix scan requires explicit future capability proposal.

**Security assertion.** Tenant/object inventory cannot leak through accidental list.

**Control assertion.** Existing Put/Open/Head/Delete remain small and portable.

### SC-45 — future list cursor is opaque and bounded

**DX.** Future lister accepts declared page limit and opaque cursor type only.

**Setup.** Target fake returns cursor tokens containing provider/key sentinels.

**Action.** Request next page with valid, tampered and cross-namespace cursors.

**Happy assertion.** Valid cursor returns bounded page without duplicate assertion.

**Edge assertion.** Invalid cursor fails safely before arbitrary provider parsing.

**Privacy assertion.** Cursor/token/key values stay out of trace/error/log defaults.

**Control assertion.** Listing remains deferred until this contract exists.

### SC-46 — list sees concurrent changes only as documented

**DX.** Future list docs state ordering/consistency rather than relying on backend.

**Setup.** Mutate object set while paginating fake fs and S3 targets.

**Action.** Collect several pages under weak/snapshot-free mode.

**Happy assertion.** Returned pages satisfy bounded item/continuation contract.

**Edge assertion.** Duplicate/missing changing objects are handled as documented.

**Safety assertion.** Client cannot infer tenant inventory beyond authorization.

**Control assertion.** No promise of total count/snapshot is implied.

### SC-47 — byte-slice helpers require size ceiling

**DX.** Optional `ReadAll` helper takes/uses explicit maximum bytes.

**Setup.** Put objects immediately below and above configured limit.

**Action.** Invoke helper and observe allocation/body close behavior.

**Happy assertion.** Below-limit control returns exact bytes and closes body.

**Edge assertion.** Above-limit result is bounded resource/size failure.

**Memory assertion.** Helper does not allocate full unbounded oversized object.

**Control assertion.** Streaming Open remains recommended/default API.

### SC-48 — content type is data, not browser policy

**DX.** PutOptions validates a declared content type separately from response code.

**Setup.** Store bytes with benign, malformed and browser-risk content types.

**Action.** Head/Open and pass Info to example download renderer.

**Happy assertion.** Store round-trips approved content type faithfully.

**Edge assertion.** Renderer chooses disposition/nosniff/security headers itself.

**Security assertion.** Storage never auto-sniffs entire untrusted stream.

**Control assertion.** Content type does not authorize an object read.

### SC-49 — object key never becomes a filesystem directory API

**DX.** Store exposes Key, not `Directory`, `Path` or raw `os.File` result.

**Setup.** Put keys with shared slash prefixes under fs and S3 fake backends.

**Action.** Inspect public Info/results and try directory-specific operation.

**Happy assertion.** All common keys work as opaque object identities.

**Edge assertion.** No core API exposes directory existence/permissions semantics.

**Compatibility assertion.** S3 common prefix cannot masquerade as directory.

**Control assertion.** Fs internals may create parents without public promise.

### SC-50 — user metadata cannot control ACL/encryption/retention

**DX.** Metadata parser reserves provider/governance/security key spaces.

**Setup.** Submit keys resembling ACL, KMS, retention, object-lock and tags.

**Action.** Validate Put options under fs and S3 adapters.

**Happy assertion.** Ordinary safe metadata control stores as declared.

**Edge assertion.** Reserved/security aliases are refused or ignored by policy.

**Security assertion.** Caller cannot escalate storage settings through map values.

**Control assertion.** Provider extensions need explicit capability/API/ADR.

### SC-51 — client-side integrity is explicit

**DX.** Integrity option names digest algorithm/expected value and size limits.

**Setup.** Wrap source with known hash; create matching and mismatching fixture.

**Action.** Execute Put through fs and S3 fake staging paths.

**Happy assertion.** Matching data returns Info after final verification point.

**Edge assertion.** Mismatch returns corrupt/source condition and cleans owned stage.

**Safety assertion.** Cleanup cannot delete a concurrent final object by key.

**Control assertion.** ETag is never substituted for declared digest.

### SC-52 — malicious read content stays caller-controlled

**DX.** Open returns raw body plus metadata; it does not parse/decompress/render.

**Setup.** Store zip-bomb-like bytes, malformed image and huge JSON fixture.

**Action.** Open/Head through core and hand body to application control.

**Happy assertion.** Core streams bytes without interpreting content.

**Edge assertion.** No automatic decompression/thumbnail/mime sniff allocates.

**Security assertion.** Application chooses scanner/parser sandbox separately.

**Control assertion.** Storage return values remain backend-neutral.

### SC-53 — backend health is not a request-path global probe

**DX.** Optional health checker is explicit and caller scheduled/owned.

**Setup.** Backend is healthy, slow, forbidden and intermittently unavailable.

**Action.** Run health probe and ordinary Put separately.

**Happy assertion.** Probe returns bounded capability/availability state.

**Edge assertion.** Ordinary operation does not block on implicit health request.

**Privacy assertion.** Health output hides endpoint/credential/raw error details.

**Control assertion.** App chooses readiness/liveness consequences.

### SC-54 — a storage client never owns application shutdown

**DX.** Adapter docs state whether injected client requires no Close by store.

**Setup.** Inject fake client recording Close/Shutdown calls.

**Action.** Construct, use and drop store; invoke app client shutdown separately.

**Happy assertion.** Store operations use client as provided.

**Ownership assertion.** Store never closes/flushes caller client unexpectedly.

**Edge assertion.** Client closed by host yields bounded operation failure.

**Control assertion.** Multiple stores can share one application client safely.

### SC-55 — untrusted key and metadata never enter default metrics

**DX.** Storage metric registry has closed operation/kind/outcome/size dimensions.

**Setup.** Execute thousands of objects with unique keys/metadata/tenant-like text.

**Action.** Collect metrics through test meter reader.

**Happy assertion.** Series count stays within declared finite cardinality budget.

**Edge assertion.** Unique values produce no new label/attribute values.

**Trace assertion.** Sampled logical traces also omit sentinels.

**Control assertion.** Backend kind/store family remains configured enum only.

### SC-56 — source error precedence is deterministic

**DX.** Error documentation defines primary versus cleanup/close failure ordering.

**Setup.** Reader errors while multipart abort/staging close also fails.

**Action.** Put and inspect returned/wrapped typed error chain.

**Happy assertion.** Primary source failure remains detectable by callers.

**Edge assertion.** Cleanup failure is bounded supplemental diagnostic, not lost.

**Privacy assertion.** Neither raw reader/provider text is automatically exported.

**Control assertion.** `errors.Is/As` behavior is stable/documented.

### SC-57 — context deadline has no invisible extension

**DX.** All operations accept caller context and no `Timeout` hidden default.

**Setup.** Backend/reader barriers exceed a short caller deadline.

**Action.** Put, Open, Delete, Promote and Sign as applicable.

**Happy assertion.** Longer-deadline control completes normally.

**Edge assertion.** Deadline outcome is returned at documented cancellable point.

**Safety assertion.** Adapter does not create background retry after caller exit.

**Control assertion.** Host may set retry/backoff policy outside core.

### SC-58 — retry belongs to a named higher-level policy

**DX.** Core Store has no transparent retry count option on Put.

**Setup.** Backend fake fails temporary before/after request acceptance variants.

**Action.** Invoke direct Put once and application retry wrapper control.

**Happy assertion.** Safe read/head retry policy can be implemented externally.

**Edge assertion.** Core write does not replay uncertain source automatically.

**Idempotency assertion.** Caller chooses CreateOnly/domain idempotency reconciliation.

**Control assertion.** Metrics/traces see actual attempts via explicit wrapper.

### SC-59 — migration reads logical references, not physical locations

**DX.** Migration worker receives domain document reference and two store handles.

**Setup.** Place object under old fs store and record new backend target mapping.

**Action.** Copy through stream/stage then update durable domain routing state.

**Happy assertion.** New reader resolves object by logical reference after cutover.

**Edge assertion.** Failed copy leaves old routing/object unchanged and recoverable.

**Privacy assertion.** Backend URLs/paths never enter domain event/API identity.

**Control assertion.** No generic `Store.MigrateAll` is required.

### SC-60 — migration rollback preserves old readable state

**DX.** Migration state machine has explicit cutover and rollback milestones.

**Setup.** Copy verified object to new store then inject new-read failure.

**Action.** Roll back routing before old-retention cleanup window ends.

**Happy assertion.** Authorized reader returns original old object via fallback.

**Edge assertion.** New partial/corrupt object is never selected as success.

**Audit assertion.** Cutover/rollback action is durable and attributable.

**Control assertion.** Destructive old deletion needs separate approved stage.

### SC-61 — migration integrity verification is explicit

**DX.** Migration options select approved verifier, not implicit ETag comparison.

**Setup.** Source/target bytes share size but differ in one byte.

**Action.** Run migration copy verification with declared digest strategy.

**Happy assertion.** Exact-match control is eligible for routing cutover.

**Edge assertion.** Same-size mismatch remains failed/retriable lifecycle state.

**Audit assertion.** Verification algorithm/result is captured safely if required.

**Control assertion.** Provider ETag does not decide cross-backend integrity.

### SC-62 — migration supports idempotent duplicate worker delivery

**DX.** Durable migration state names operation identity and terminal results.

**Setup.** Deliver same migration task twice concurrently to two workers.

**Action.** Copy/promote/update routing under conditional store semantics.

**Happy assertion.** One durable final routing decision results.

**Edge assertion.** Loser observes existing/claimed state without destructive redo.

**Storage assertion.** No duplicate final key overwrite by default.

**Control assertion.** Exactly-once queue delivery is not assumed.

### SC-63 — backup restore has a cross-system runbook

**DX.** Documentation names object bytes, DB references, audit and config owners.

**Setup.** Restore object backend to an earlier point than domain database fixture.

**Action.** Run documented reconciliation/read test procedure.

**Happy assertion.** Compatible restore control reconstructs authorized references.

**Edge assertion.** Mismatch is surfaced as lifecycle/integrity problem, not hidden.

**Safety assertion.** Restore does not blindly delete newer object data.

**Control assertion.** Store API makes no automatic point-in-time claim.

### SC-64 — object lifecycle does not localize logical key

**DX.** UI can format localized display name independently from storage Key.

**Setup.** Same object has English/Kazakh/Russian display metadata in domain DB.

**Action.** Read/write object through all locale contexts.

**Happy assertion.** Parsed storage key remains byte-identical across locales.

**Edge assertion.** Translation fallback cannot cause a new physical object key.

**Audit assertion.** Audit uses stable subject/action code, not translated copy.

**Control assertion.** OTel metric vocabulary remains language-neutral.

### SC-65 — event payload stores logical document reference only

**DX.** Event schema has domain document ID/reference field, no backend URL type.

**Setup.** Create an upload lifecycle event from fs then S3 target configurations.

**Action.** Serialize event and replay application fixture after backend switch.

**Happy assertion.** Same event schema resolves object through current domain routing.

**Edge assertion.** No event includes bucket/path/signed URL/SDK response.

**Replay assertion.** Historic replay triggers no upload/download side effect.

**Control assertion.** Storage migration does not force event rewriting.

### SC-66 — tenant-scoped store cannot escape prefix/root

**DX.** Tenant adapter supplies opaque authorized scoped Store, not raw prefix.

**Setup.** Resolve tenant A store and use crafted valid-looking key for tenant B.

**Action.** Put/Open/Head/Delete through scoped adapter.

**Happy assertion.** Tenant A control keys map only inside its authorized scope.

**Edge assertion.** Crafted key never reaches B backend location.

**Telemetry assertion.** Tenant/prefix values remain absent from default signals.

**Control assertion.** Plain Store has no public TenantID override parameter.

### SC-67 — db-per-tenant store client lifecycle is bounded

**DX.** Resolver/pool owns a bounded cache and explicit eviction/close policy.

**Setup.** Resolve thousands of tenant stores against fake datasource/backend clients.

**Action.** Exercise active, idle, disabled and evicted tenant sequences.

**Happy assertion.** Active configured tenant reuses/obtains correct client safely.

**Edge assertion.** Cache does not grow unbounded or close active caller client.

**Safety assertion.** Resolver error never falls back to another tenant backend.

**Control assertion.** Core storage remains unaware of tenant database names.

### SC-68 — cross-tenant maintenance is named and bounded

**DX.** Admin maintenance accepts explicit authorized tenant cohort/cursor policy.

**Setup.** Create two tenant scopes plus one super-admin maintenance capability.

**Action.** Run a bounded migration/cleanup batch.

**Happy assertion.** Ordinary tenant scope cannot request cross-tenant operation.

**Edge assertion.** Admin failure/cancellation records exact bounded progress state.

**Audit assertion.** Actor/purpose/cohort action is durably recorded per policy.

**Control assertion.** No nil scope means “all tenants.”

### SC-69 — key grammar tightening has legacy reader plan

**DX.** Release manifest identifies old accepted legacy keys and new grammar.

**Setup.** Seed historic key accepted by v1 but refused by v2 parser.

**Action.** Run v2 read/migration/write fixtures.

**Happy assertion.** New writes reject old unsafe grammar consistently.

**Edge assertion.** Authorized legacy read maps exactly or fails visibly, never aliases.

**Migration assertion.** Re-key/copy process has durable reference update plan.

**Control assertion.** Parser does not silently normalize historic key differently.

### SC-70 — metadata schema release preserves historic readers

**DX.** Metadata manifest versions key names/types/deprecations explicitly.

**Setup.** Store object with old schema then introduce renamed/new key fixture.

**Action.** Head/Open via old/new application reader fixtures.

**Happy assertion.** New reader understands old representation or default rule.

**Edge assertion.** Old reader does not misinterpret new security-sensitive value.

**Release assertion.** Compatibility note and migration test accompany change.

**Control assertion.** Unknown metadata never controls store semantics.

### SC-71 — S3 SDK upgrade is a contract event

**DX.** Adapter release record names selected SDK/version and target test date.

**Setup.** Run same real-target conformance suite before/after candidate upgrade.

**Action.** Compare normalized results, error mappings and multipart behavior.

**Happy assertion.** No semantic differences pass without deliberate review.

**Edge assertion.** Changed retry/error/ETag behavior updates capability contract.

**Safety assertion.** Upgrade test credentials/cleanup remain scoped and explicit.

**Control assertion.** Core public API never exposes SDK types.

### SC-72 — provider configuration drift is detected safely

**DX.** Optional verifier/health check reports named capability drift states.

**Setup.** Change test bucket versioning/policy/object-lock setting after bootstrap.

**Action.** Invoke affected capability and scheduled health check.

**Happy assertion.** Unchanged target control retains declared capability behavior.

**Edge assertion.** Drift returns unsupported/forbidden without false feature claim.

**Observability assertion.** Signal uses bounded capability/outcome, no bucket ID.

**Control assertion.** Store does not mutate its public capabilities per request.

### SC-73 — process shutdown leaves no unowned goroutine

**DX.** Store construction has no hidden background cleanup/publish worker.

**Setup.** Construct/use/drop fs/S3 stores under goroutine leak test harness.

**Action.** Cancel caller contexts and close application-owned clients.

**Happy assertion.** Ordinary operation has no lingering adapter goroutine.

**Edge assertion.** Explicit maintenance/reconciler has caller-owned lifecycle.

**Ownership assertion.** Store does not flush/retry after application shutdown.

**Control assertion.** Resource cleanup follows body/client ownership contracts.

### SC-74 — storage release checklist is executable

**DX.** CI collects registry/schema/conformance/benchmark artifacts per release.

**Setup.** Simulate release changing one key rule, capability or error mapping.

**Action.** Run release validation pipeline/fixture set.

**Happy assertion.** Additive compatible change documents affected target cells.

**Edge assertion.** Breaking change fails without migration/compatibility evidence.

**Security assertion.** Privacy corpus and destructive-cleanup scope checks rerun.

**Control assertion.** Release cannot claim untested provider support.

---

## Implementation and release review checklist

Each storage pull request must answer every item below with a linked test,
decision or explicit deferral. This checklist is intentionally repetitive with
the scenarios: it gives reviewers a fast gate while the scenarios preserve the
executable evidence behind each answer.

### API and ownership

- Does a public operation accept parsed opaque Key rather than raw path/URL?
- Does the operation stream input/output without unbounded buffering?
- Is every reader/body/client/stage ownership and close responsibility named?
- Does the method avoid closing caller-provided source or client?
- Does an error preserve caller context cancellation and source failure meaning?
- Is there one validated write/delete/promotion mode rather than Boolean soup?
- Are unsupported backend semantics represented by a capability/refusal?
- Does return `Info` hide backend handle, URL, physical path and raw response?
- Is any byte-slice helper bounded by declared maximum object size?
- Can external users avoid concrete adapter type assertions for supported work?

### Key, metadata and privacy

- Does grammar refuse traversal, absolute, separator, control and over-limit form?
- Is Windows/case-insensitive filesystem ambiguity covered even on Linux CI?
- Is root/prefix joining performed exactly once after parsing?
- Are all metadata keys/values/counts/total bytes validated before transfer?
- Are reserved security/ACL/encryption/retention keys excluded from metadata map?
- Are object keys, buckets, endpoints, version IDs and URLs absent from signals?
- Is raw provider error text behind a controlled diagnostic boundary only?
- Is a stable keyed hash avoided unless a dedicated privacy ADR authorizes it?
- Are signed URLs treated as secret bearer capabilities in formatting/log tests?
- Does a hostile sentinel corpus cover source, metadata, provider and error paths?

### Filesystem safety

- Does fs adapter have a documented supported platform/filesystem secure mode?
- Is final visible write staged and placed only after complete successful copy?
- Can symlink/hard-link/root replacement races escape configured root?
- Is CreateOnly a real single-winner operation, not existence precheck plus write?
- Are file/directory mode, umask and same-filesystem rename assumptions explicit?
- Is fsync/rename/directory sync completion meaning stated without backup claim?
- Can stale stage cleanup identify only owned resources with bounded age/scope?
- Does cancellation/error leave no partial final object or descriptor leak?
- Are external local administrator mutations outside store's false integrity claim?
- Is test fake explicitly not used as evidence for filesystem security semantics?

### S3 compatibility and operations

- Is selected S3 SDK isolated outside core and fs-only dependency graph?
- Does caller own client, credentials, endpoint, TLS, retries and shutdown?
- Is every claimed S3 operation validated against real MinIO and selected clouds?
- Are AWS/R2 claims recorded as target-specific capability evidence, not branding?
- Does multipart abort owned upload/session safely on source/context failure?
- Does completion-response loss remain uncertain rather than silently successful?
- Is ETag documented opaque and separate from integrity checksum semantics?
- Are conditional operations unavailable when provider semantics cannot prove them?
- Does adapter avoid auto-retry uncertain writes and signed-url logging?
- Does provider configuration drift produce bounded error/capability evidence?

### Cross-framework composition

- Does OTel integration exist only as optional satellite decorator?
- Are logical storage spans distinct from optional S3 HTTP/SDK spans?
- Does audit retain authorized object identity only in protected durable store?
- Are tenant scope/prefix/root mappings established before Store reaches caller?
- Does event/source workflow use staged saga/reconciliation rather than 2PC fiction?
- Are i18n display names kept out of opaque keys and event/schema identity?
- Does migration operate on logical domain references and durable lifecycle state?
- Are background reconciliation/maintenance workers explicit, idempotent and bounded?
- Does storage removal leave policy, transaction and domain correctness unchanged?
- Is no future satellite pulled into root/core merely for an integration shortcut?

### Compatibility and rollout

- Does release classify change as additive, deprecation, rename or break?
- Are key/metadata/capability/error schema snapshots diffed in CI?
- Is legacy key/metadata reader/migration/rollback behavior described?
- Has rolling upgrade old/new adapter compatibility been fixture-tested?
- Are cloud/SDK/provider version and test date in release record?
- Does migration plan include integrity, idempotency, audit, rollback and retention?
- Are destructive cleanup targets exact, validated, rate/batch bounded and recoverable?
- Is backup/restore cross-system consistency owned by a concrete runbook?
- Are benchmark/goroutine/resource-leak gates recorded for large stream paths?
- Does finished release omit all unsupported feature claims from documentation?

## Exit evidence summary

The storage roadmap can transition from proposed to implementation-ready only
after key grammar and stream/write contracts receive a decision record, secure
fs tests run on advertised platforms, selected S3 SDK/targets pass declared
conformance cells, and the application-facing migration/tenant/audit/telemetry
boundaries above are accepted. `List`, broad provider administration, lifecycle
governance, automatic retry, generic client encryption and a migrator CLI remain
explicitly outside that first acceptance scope.

## Conformance execution protocol

The same test source must run in layers. A green local fake alone is useful for
fast feedback but never evidence for filesystem race safety or S3 provider
semantics. Every release report identifies the layer that produced each result.

### Layer 1 — pure contract and fuzz tests

- Run key grammar parser/property corpus without a backend.
- Run options/metadata limit and contradictory-mode validation tests.
- Run error projection and hostile sentinel privacy scans.
- Run stream ownership with reader/body fault-injection fakes.
- Run state-machine tests for stage/promote/abort transitions.
- Run metric cardinality and OTel no-op/sampled equivalence fixtures.
- Run all cases with race detector where context/maps/handles participate.
- Record seed for a failing fuzz corpus and add it as a regression case.

### Layer 2 — secure filesystem integration tests

- Create one explicit temporary root per test, never use working directory.
- Validate root containment before each destructive cleanup action.
- Exercise staging, visibility, CreateOnly and cancellation under real file I/O.
- Exercise permission/full-disk/fault paths with platform-appropriate fixture.
- Exercise symlink/root replacement attacks where advertised platform allows it.
- Exercise file/directory sync order only through deterministic abstraction/fake.
- Check descriptor/goroutine leaks over repeated Open/Close and failed writes.
- Report unsupported platform semantics as skipped-with-reason, not passing.

### Layer 3 — MinIO S3 integration tests

- Create isolated client, bucket and randomized validated test prefix.
- Run basic Put/Open/Head/Delete and metadata corpus on a real S3 API server.
- Run multipart threshold, source failure, abort and completion-response fault tests.
- Run target-supported conditional/create/delete/signing cells explicitly.
- Capture MinIO/server, SDK and adapter versions in machine-readable report.
- Cleanup only the resolved test prefix; retain failed artifacts under policy.
- Avoid asserting AWS/R2 behavior solely because MinIO passes a cell.
- Keep endpoint/credential/key values out of test snapshot/output artefacts.

### Layer 4 — cloud target integration tests

- Run AWS and R2 suites in distinct isolated accounts/projects/configurations.
- Grant minimum scoped credentials and rotate them through secret-managed CI.
- Mark each unavailable target cell `unverified`, never `passed` by fallback.
- Cover endpoint-specific signing, conditions and version behaviour actually used.
- Test lost response/transient behavior with controlled proxy/fault fixture if safe.
- Enforce lifecycle/cost guardrails and exact cleanup prefix validation.
- Capture provider test date, SDK version, region/profile and known divergence.
- Review every new “compatible” marketing/documentation statement against report.

### Layer 5 — cross-domain synthetic tests

- Drive authorized tenant scope into a scoped store and attempt cross-scope key.
- Run domain transaction/outbox/staging/promote crash matrix with duplicate worker.
- Attach optional audit revision and prove unsampled tracing leaves it complete.
- Render localized display name while asserting storage key stays language neutral.
- Migrate a logical reference fs → S3 and exercise rollback before cleanup.
- Read an event-store domain reference without replaying storage side effects.
- Confirm OTel logical span has only allowed storage vocabulary.
- Confirm every cross-domain failure has an owner and no fake distributed commit.

### Layer 6 — performance and operational checks

- Benchmark no-op, sampled and metrics-enabled common stream operations.
- Benchmark unknown-size large streams without object-sized allocation growth.
- Benchmark key/metadata validation under hostile input bounds.
- Measure multipart part count/overhead under documented configuration.
- Measure fs descriptor cleanup and S3 connection reuse under concurrent reads.
- Verify client/pool lifecycle remains host-owned in long-running process tests.
- Verify maintenance/reconciliation batch limits and cancellation behavior.
- Publish baseline and reject unexplained regression before satellite release.

## Explicit initial non-goals

- a generic filesystem browser or directory API;
- S3 bucket policy/ACL/KMS/admin/lifecycle management wrappers;
- server-side copy/move claim as portable atomic rename;
- arbitrary metadata/headers/options passthrough to selected SDK;
- automatic retries for uncertain writes or transparent resumable upload API;
- universal checksum, encryption, legal-hold or secure-erasure guarantee;
- recursive list/delete, inventory/count or migration-all convenience calls;
- object-store-as-database/event-store/authorization system;
- global default storage client, credentials or endpoint configuration;
- automatic tenant extraction from a header or trace/baggage value;
- raw object/provider diagnostic capture in vv telemetry;
- production support statement for an S3 target without current real test evidence.

## First implementation backlog

The following order protects the public contract from premature capability
surface. Every line is a deliverable with a test, rather than an invitation to
add a package because a backend SDK happens to expose a method.

1. Decide/record `Namespace`, `Key`, key grammar and safe diagnostic policy.
2. Define core `Store`, `Info`, `PutOptions`, typed conditions and stream owners.
3. Build pure parser/options/error/privacy/ownership test harness.
4. Implement a deterministic in-memory fake for contract unit tests only.
5. Implement secure POSIX filesystem direct Put/Open/Head/Delete with staging.
6. Prove fs CreateOnly, failure visibility, root and symlink containment suite.
7. Decide selected S3 SDK in a dependency ADR, with injected client ownership.
8. Implement basic S3 Put/Open/Head/Delete against MinIO first.
9. Add multipart success/abort/uncertain-response conformance and real-target run.
10. Add a conditional-create capability only after a backend-proof matrix exists.
11. Add metadata/content type with exact portable limits and release manifest.
12. Add optional OTel storage decorator only after OTel O-13 vocabulary test passes.
13. Add storage audit decorator only after audit transactional policy is accepted.
14. Write document stage/promote state-machine fixture with PostgreSQL/outbox fake.
15. Decide whether a real use case justifies public `Stage`/`Promote` capability.
16. Add S3 signing behind explicit capability and sensitive-value redaction suite.
17. Run AWS/R2 target matrix and publish exact capability limitations.
18. Design tenancy-scoped adapter after tenancy resolver topology is stable.
19. Publish reference fs-to-S3 migration state machine and rollback test fixture.
20. Defer every non-goal until its own ADR, consumer demand and test matrix exist.

The storage satellite is ready to begin implementation at item 1; it is ready
for a stable public beta only once items 1–11 pass their evidence gates. Items
12–19 are intentional cross-framework integrations, each blocked by the
corresponding roadmap rather than by a speculative import into storage core.

## Reviewer quick-stop rules

Stop and request a decision rather than merging when a storage change:

- adds an S3/AWS/MinIO/R2 dependency to core or fs-only module;
- accepts a raw filesystem path, object URL, bucket or provider request map;
- formats a key, version, signed URL, metadata or raw SDK error by default;
- writes directly to visible fs destination before full stream success;
- implements conditional write with an existence/Head precheck;
- treats remote completion-response loss as a guaranteed failed/successful write;
- calls a provider ETag a portable integrity checksum;
- provides a generic recursive `List`, `DeleteAll`, `Move` or `MigrateAll`;
- starts hidden retry, cleanup, publisher or health goroutines;
- adds tenant routing, actor policy, audit values or translated text to core;
- claims AWS/R2/MinIO support without the corresponding real-target report;
- turns an adapter-specific security/governance feature into untyped options.

Every stop rule is resolved by an explicit capability/ADR, an adapter boundary or
an intentional non-goal—not by a convenience flag. That discipline lets a
filesystem test implementation and a production S3-compatible deployment share
the safe contract without pretending their operational reality is identical.

## Final storage boundary

The public core promises a safe object contract, not a generic cloud platform.

It can grow only by adding named capabilities with evidence.

It cannot grow through raw provider escape hatches.

It cannot make durability, authorization or compliance promises it cannot test.

It cannot make a remote object transactionally equal to PostgreSQL.

It cannot make an object key a substitute for a domain model.

It cannot make telemetry/audit a substitute for actual data correctness.
