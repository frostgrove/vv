# Roadmap — the error subsystem

**Status:** phase 0 done — MariaDB, the captured corpus, the nine decisions.
Phases 1–9 are proposed. What is built is marked in §14, and §6's engine matrix
is now measurement rather than design input.
**Scope:** one cross-dialect error contract, its four dialect implementations, a
per-database schema catalog, a multi-violation probe engine, a transport-neutral
port layer, and the renderers and decorators that put a public payload on the
wire.

This is one of two roadmaps. [The framework roadmap](ROADMAP-framework.md) says
where code lives and what it may import; this one says what a failed write tells
the client. Every package proposed below lands in that structure, and the module
decisions there — including `errs` getting its own version line — are assumed
here rather than re-argued.

This is the largest single piece of work planned here, and it is not only about
this repository. The contract half is meant to outlive it and become the error
standard for every Go service in the ecosystem — the way JPA is a set of
interfaces and conventions that Hibernate then implements. The split is
deliberate and it runs through every section below:

| | JPA's half | Hibernate's half |
|---|---|---|
| what it is | interfaces, conventions, value types | parsers, catalogs, probes, renderers |
| where it lives | `errs/` | `errs/sqlerr/`, `catalog/`, `probe/`, `port/`, `http/*`, `adapter/*` |
| what it depends on | the standard library, nothing else | whatever it must |
| who imports it | every service in the ecosystem, including ones with no database | consumers of this library |

---

## 1. Why

Today a duplicate key reaches a client as this:

```json
{"error":"conflict","message":"conflict: ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
```

Three things are wrong with it, and all three are already written down as gaps in
[docs/usecases/Index.md](docs/usecases/Index.md):

- **Gap 16.** Every status but 500 echoes the error's own text. For a 409 that
  text is the driver's — a constraint name, a column name, a SQLSTATE, and the
  driver's own prefix, handed to whoever asked.
- **Gap 3** — which belongs to [[UC-004]], not [[UC-015]]. It has two halves and
  only the second is in scope. The first, the gate's own existence probe that
  deliberately runs *without* the narrowing so a save cannot overwrite an
  invisible row, is shipped, tested and untouchable by this work. The second is
  that a create colliding on a unique index over a column the caller cannot see
  answers 409 about a row the scope hides from them. This work narrows the second
  half and does not move the first, so gap 3 does not close.
- **Gap 22.** There is no transport-neutral layer above the repository. Gap 22
  argues the opposite and deserves an answer: *"writing the third binding is the
  evidence, because it needed nothing added to the shared package."* That is true
  and it is not the claim here. `crudhttp`'s split survived a third **HTTP**
  binding because all three speak status codes and JSON bodies. gRPC does not,
  and neither does a queue consumer. What has to move is not the status table —
  it is the input DTO, which §3 shows is also the only place a `field` path can
  come from.

And a fourth thing, which is not a gap because nobody promised otherwise: the
client is told about one problem at a time. A form with a taken email, a
non-existent organisation and an under-age user is three round trips.

The fix is one subsystem, not four patches:

1. Parse the driver's error properly, per dialect, into something structured.
2. Find the *other* violations the same payload would cause.
3. Name the field the way the **client** named it, not the way the database does.
4. Render a payload that says everything useful and nothing internal.

---

## 2. The shape of the answer

A client `POST`s this:

```json
{
  "smth": "smth",
  "user": { "email": "test@example.com", "org_id": 42, "age": 15 }
}
```

against a `users` table with a unique `email` (taken), a foreign key
`org_id → orgs.id` (42 does not exist), and a `CHECK (age >= 18)`.

It gets back **one** response:

```json
{
  "type": "error",
  "errors": {
    "validation": [
      { "field": ["user", "email"],  "error_code": "unique",      "message": "user with this email already exists" },
      { "field": ["user", "org_id"], "error_code": "foreign_key", "message": "the organisation does not exist" },
      { "field": ["user", "age"],    "error_code": "check",       "message": "age must be at least 18" }
    ]
  }
}
```

Everything in this roadmap exists to make that response possible, correct, safe
and cheap. Read the rest as an answer to "what does that actually take".

Note what is *not* in it: no constraint name, no table name, no column name, no
SQLSTATE, no driver prefix. `field` is the path the client sent, `error_code` is
stable and machine-readable, `message` came from a registry rather than from a
driver. The rich version of all of that still exists — the `Detail` on the
internal error carries the SQLSTATE, the constraint, the table, the columns and
the driver error underneath — it is not what goes on the wire.

### The envelope

```
{ "type": "error", "errors": { "<group>": [ { "field": Path?, "error_code": Code, "message": string } ] } }
```

- `type` is always `"error"`. It exists so a client can branch before parsing.
- `group` is `"validation"` when the violation names a field, `"general"` when it
  does not. That is why the 409 above appears under `validation`: the group
  describes *what the client can act on*, not the status.
- `field` is an array of strings and integers — `["user","email"]`,
  `["items",3,"email"]`. Omitted for a violation with no field.
- A 500 emits `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}`.
  One uniform shape for clients to parse, and no message, so [[D-015]]'s silence
  rule holds by construction rather than by a `case` in a switch.

This is the **only** envelope the library ships. The current
`{"error":…,"path":…,"message":…}` body is dropped. RFC 9457 problem+json is not
shipped either. The `Renderer` seam exists so a consumer can write either in a
few lines, and the reason for shipping one shape is that two shapes is twice the
surface to test, document and keep honest, for a choice almost nobody changes.

### The status table

| `Kind` | Status | Codes that land there |
|---|---|---|
| `KindValidation` | **422** | `required`, `check`, `too_long`, `out_of_range`, `invalid_format`, `invalid_enum`, and every business code a service layer declares |
| `KindConflict` | 409 | `unique`, `foreign_key`, `restrict`, `stale_version`, `not_unique`, `exclusion` |
| `KindNotFound` | 404 | `not_found` |
| `KindForbidden` | 403 | `forbidden` |
| `KindUnauthorized` | 401 | `unauthenticated` |
| `KindBadRequest` | 400 | `malformed_body`, `invalid_id`, `unknown_field`, `bad_query` |
| `KindRetryable` | 503 + `Retry-After` | `deadlock`, `serialization_failure`, `lock_timeout`, `unavailable` |
| `KindInternal` | 500 | `internal` — and no message |

422 is new. 400 keeps exactly the meaning it has now: the request itself was
malformed — a body that would not decode, an id that would not parse, a query
document naming a field the model lacks. 422 is for a request that was
well-formed and whose *values* were rejected. A client can tell "I sent nonsense"
from "I sent something you refused", which it cannot do today.

**This moves two statuses, and that is a breaking change, not a refinement.**
NOT NULL and CHECK are `23502` and `23514` — class 23 — so today the adapters
wrap them in `crud.ErrConflict` and every one answers **409**. Putting `required`
and `check` in the 422 row means the adapters stop wrapping those two states in
`ErrConflict`, which is a change to [[D-015]]'s own table and needs its successor
(D-046), not a footnote. Shipping both mappings — 409 from the sentinel, 422 from
the `Kind` — is the one outcome that must not happen.

Class 22 needs its own answer rather than silence, because D-015 already gave
one: *"class 22 (data exception) is a coercion bug."* `too_long` from a client
value that genuinely exceeds the column is a 422; a `22P02` from a value rx-crud
itself coerced is a 500. The classifier cannot tell those apart from the SQLSTATE
alone, so the rule is the write path: a value that arrived in the payload is the
client's, and one the library produced is ours.

**Precedence when one fault mixes kinds**, highest first:

```
Internal → NotFound → Forbidden → Retryable → Conflict → Validation → BadRequest
```

The order is by how much the answer must conceal or defer:

- **Internal first**, and narrowly. If the *original classification* failed, the
  set is incomplete and possibly misleading, and 500 with nothing in it is the
  truthful answer. This does **not** cover a failed probe: enrichment failing
  must never downgrade a correct 409 into an opaque 500 (§8).
- **NotFound before Forbidden** is [[D-008]] verbatim: never confirm that a
  hidden row exists.
- **Conflict before Validation** because a collision is a fact about the world
  the client cannot fix by editing its own values, while a validation failure is
  purely about the payload. The envelope still carries every violation, so
  nothing is lost — only the coarse status is a single value.

The alternative — Validation wins, so a mixed payload reads as 422 — is
defensible and is offered as a policy knob rather than argued away.

---

## 3. The layering

The repository must not know about HTTP, and the HTTP handler must not know about
the repository's shape. A gRPC binding is coming, input DTOs differ per
transport, and a hand-written service layer has to be slottable in between.

```
fiber handler ─┐
gin handler   ─┼─→ generic http layer ─→ resource adapter ─→ service ─→ repository ─→ crudsql/crudpgx ─→ driver
net/http      ─┘   status · envelope ·    transport DTO ⇄     rules ·     SQL              classify
grpc handler  ───→ generic grpc layer     command · inverse    orchestration
                   code · status detail   path map
```

Read as the owner put it: *"input DTOs and data may differ for different HTTP
providers and for gRPC; adapters exist to bring data into the shape the service
accepts, and the service then works with repositories."* The chain as stated was
`fiber handler → generic http handler → http adapter → …`; this roadmap reads the
tail as `→ service → repository`, and says so here so a wrong reading is visible
rather than buried.

### Path translation is a chain, one hop per layer

This is the load-bearing idea of the whole design, and the answer to "how does a
database column become `["user","email"]`". Each layer translates only the hop it
*already knows about*. Nothing guesses.

| Hop | Owner | Why it can |
|---|---|---|
| constraint / table / column → model field | the fault decorator, through `crud.Meta` | `Meta` binds a `Schema` to a table; `Schema` alone is table-independent and cached per type, so it cannot tell two databases' `users` apart |
| model field → service field path | the service | it defined its own command shape |
| service field path → **transport input path** | **the resource adapter** | it *is* the mapping — it performed it a moment ago while decoding |
| path → wire rendering | the generic transport layer | JSON array, JSON Pointer, proto field path |

The adapter can invert its own mapping because the mapping is the adapter. And
when the adapter is generated (§10), the inverse map is generated with it — so a
column the DTO does not cover is a build-time or start-up failure, never a
request-time bad path. That is [[D-021]] applied to the one part of this design
most likely to rot.

The same fault therefore renders `["user","email"]` on the REST API and
`user.email` on gRPC, with no per-transport error code anywhere.

### The fallback, for handlers nobody generated

A hand-written endpoint has no mapper. For those, the generic HTTP layer keeps a
reference to the raw request bytes and, **only when a fault occurs**, walks them
into a leaf-path index, matching on the folded key name and, where the driver
gave one, the offending value. Zero cost on the happy path.

Retaining them costs **a copy, not a reference**, and the roadmap has to say so
because the obvious version is a use-after-free. Fiber documents `c.Body()` as
*"valid only within the handler … don't store direct references to the returned
data"*, and `crudfiber` builds its app with a plain `fiber.New()`, so Immutable is
off. Two further facts: no Fiber *write* route calls `c.Body()` at all — `Create`,
`Update`, `Replace` and `BulkDelete` all go through `c.Bind().Body()` — and
[`crudhttp.DecodeJSON`](http/crudhttp/request.go) is a pure function whose
`io.ReadAll` result dies at return, so there is nowhere in `crudhttp` to hang a
reference either.

This needs a named carrier: a `DecodeJSONKeep` returning the bytes alongside the
decode, used by all three bindings, and one `[]byte` copy per write request,
capped. That is a real cost on the happy path and it is the price of the fallback
working at all.

Two honest limits, stated here rather than discovered later:

- It is JSON only. Fiber's `Bind().Body()` dispatches on Content-Type and accepts
  XML and form encodings ([[D-034]]), and a form body has no nesting to index. A
  non-JSON body degrades to the model field name.
- Value matching disambiguates repeated key names in nested or array payloads.
  Where the driver gives no value — SQLite's foreign-key error gives nothing at
  all — the index falls back to the first key that folds to the column name, and
  marks the path approximate.

The fallback is the fallback. The generated inverse map is the mechanism.

### What the existing code already gives us

Read before designing around it:

- **Every route already funnels through one place.**
  `h.fail(c, err)` → `h.opt.errorHandler` at
  [handler.go:369](http/crudfiber/handler.go#L369). The new rendering attaches
  there and no route is touched.
- **`Create` binds the body straight onto the model** —
  [handler.go:188](http/crudfiber/handler.go#L188). There is no separate input
  DTO today, so today's "input path" *is* the model's JSON shape. The adapter
  layer is what introduces a distinct one, which is why the owner's layering
  request and the `field` requirement are one piece of work rather than two.
- **`Save` and `Delete` take no options**, so `WithScope` reaches reads only —
  said outright in [options.go](http/crudfiber/options.go). A scope-aware probe
  cannot inherit a transport scope; its predicate has to come from the
  `security.Policy`.
- **`Replace` issues a `GetByID` first when the key is auto** —
  [handler.go:246](http/crudfiber/handler.go#L246) — so a PUT can 404 before any
  constraint is reached.
- **The hardest bookkeeping question the probe has is already answered.**
  `crud.UpdatePlan` ([crud/update.go:34](crud/update.go#L34)) is built at
  `Define` time "so a broken DTO fails at start-up rather than on the first
  request", and it resolves a DTO into `[]Change{Field, Value}` with a `planKind`
  per field — always applied, applied when non-nil, applied when defined. The
  repository computes that list on every update. The probe takes the changes it
  is handed instead of re-deriving presence from the DTO, and `PlanFor`'s
  build-at-declaration pattern is exactly the model the inverse path map copies.

---

## 4. Packages and modules

```
ROOT MODULE  github.com/shardit-io/vv               (no third-party requirement)
├── crud/                     unchanged — every sentinel stays exactly as it is
├── errs/                     NEW  the contract. stdlib only. phase 1.
├── errs/sqlerr/              the corpus types are HERE ALREADY (phase 0); the four
│                             dialect parsers join them at phase 2. pure functions,
│                             stdlib only. The corpus lives in this module rather
│                             than in test/ because the parsers are unit-tested and
│                             a unit test here cannot import the test module — the
│                             generator, which needs the drivers, goes the other way.
│                             ↑ the whole of errs/ becomes its own module at the
│                               first tag, not before: see D-036, which measured why.
├── catalog/                  NEW  per-database schema catalog and introspection
├── probe/                    NEW  Simple and Full violation handlers
├── port/                     NEW  commands, Service, Mapper, the path chain
├── repo/decorators/faults/   NEW  the decorator that enriches integrity errors
├── adapter/crudsql/          extended — richer by-shape extraction
├── http/crudhttp/            extended — Kind→status, the envelope, the renderer seam
├── http/crudnet/             extended — error middleware; stdlib only, so it stays here
└── cmd/vv/                 extended — DTO, mapper + inverse map, service, wiring

UNPUBLISHED
└── test/corpus/       + the four drivers   provokes every violation and captures it
    test/cmd/corpus/                        `make corpus`

SATELLITES — one dependency decision each, per [[D-033]]
├── http/crudfiber/    + fiber/v3   error middleware, shell over `port`
├── http/crudgin/      + gin        the same, ported one to one
├── adapter/crudpgx/   + pgx/v5     typed extraction from *pgconn.PgError
└── rpc/crudgrpc/      + grpc       NEW, phase 9
```

Three module facts, and the framework roadmap argues the first two — they are
repeated here only because every package above depends on them:

- **`crud/` still imports the standard library and nothing else.** That half of
  [[D-016]] survives [[D-033]] and it decides the dependency direction here:
  `crud` cannot import `errs`. `errs` imports nothing. Everything else imports
  both. The `crud` sentinel a `Fault` wraps is attached by the *caller* — the
  adapter or the decorator, which already import both — so `errs` never needs to
  name `crud.ErrConflict`.
- **The root module is `github.com/shardit-io/vv`** once the framework
  roadmap §11's second rename lands; every path above is written against that.
- **`errs` gets its own module, and [[D-033]] is amended to allow it.** Its
  invariant becomes *no **third-party** requirement* rather than *no external
  requirement at all*. The distinction is the one D-033's own reasoning rests on:
  its case is MVS floors — *"gin pulls sonic, validator/v10, quic-go, protobuf and
  mongo-driver; a consumer on Fiber has no business having any of them take part
  in its version selection"* — and a first-party zero-dependency module
  contributes one line and no transitive graph at all. The letter forbade it; the
  reason never did.

  What decided it is release cadence, not tidiness. `errs` is the one subsystem
  meant to be adopted by services that have no database, and D-033 rule 4 puts
  every module on one version — so a team standardising it across forty services
  would take a version bump from every CRUD bugfix in a library thirty-eight of
  them do not use. **Lockstep couples cadence across subsystems with nothing to do
  with each other, and it lands hardest on the one with the widest audience.**
  It also had a deadline: the framework roadmap §11 says the first tag is the point of no return, and
  module boundaries are far harder to move afterwards than names.

This is the the framework roadmap tier structure applied to one subsystem. `Makefile:MODULES`
gains `rpc/crudgrpc` when phase 9 lands, and every existing
target loops over it for free.

---

## 5. The contract — `errs`

Transport-neutral, storage-neutral, dependency-free. It must serve a SQL
constraint violation, a hand-written business rule, a gRPC call, a queue consumer
and a validation library bridge without smelling of any of them.

### Codes

Stable, machine-readable, and the thing a client branches on. Constants for the
standard set; a consumer declares its own of the same type.

```go
type Code string

const (
    // storage-shaped
    CodeUnique       Code = "unique"
    CodeForeignKey   Code = "foreign_key"     // the parent row is absent
    CodeRestrict     Code = "restrict"        // children still reference this row
    CodeRequired     Code = "required"        // NOT NULL
    CodeCheck        Code = "check"
    CodeExclusion    Code = "exclusion"
    CodeTooLong      Code = "too_long"
    CodeOutOfRange   Code = "out_of_range"
    CodeInvalidFormat Code = "invalid_format"
    CodeInvalidEnum  Code = "invalid_enum"
    CodeStaleVersion Code = "stale_version"

    // request-shaped
    CodeMalformedBody Code = "malformed_body"
    CodeInvalidID     Code = "invalid_id"
    CodeUnknownField  Code = "unknown_field"
    CodeBadQuery      Code = "bad_query"

    // decision-shaped
    CodeNotFound        Code = "not_found"
    CodeForbidden       Code = "forbidden"
    CodeUnauthenticated Code = "unauthenticated"

    // infrastructure-shaped
    CodeDeadlock             Code = "deadlock"
    CodeSerializationFailure Code = "serialization_failure"
    CodeLockTimeout          Code = "lock_timeout"
    CodeUnavailable          Code = "unavailable"
    CodeInternal             Code = "internal"
)
```

A code is declared once with its `Kind` and its default message, in an
`errs.Codes` **value** that is wired in like everything else in this package — not
in a package-level table mutated by imports. The distinction matters and an
earlier draft got it wrong: a process-wide registry that panics on double
registration is exactly the `init()`-time global the SPI section refuses four
paragraphs later, and two libraries each declaring `too_long` with different
kinds would kill the binary depending on link order. `Codes.Add` returns an error
for a redeclaration with a different `Kind`, and the wiring decides whether that
is fatal.

### Kind

The transport class. **Transports map `Kind`, never `Code`**, which is what lets a
consumer invent fifty codes without touching a status table.

### Path

An array of names and indices, because that is the shape every framework that
got this right converged on — Bean Validation's `Path.Node`, Zod's `path`, DRF's
nested error structure.

```go
type Step struct { Name string; Index int; IsIndex bool }
type Path []Step

func (p Path) MarshalJSON() ([]byte, error)  // ["items",3,"email"]
func (p Path) String() string                // items[3].email        — for logs
func (p Path) Pointer() string               // /items/3/email        — RFC 6901
```

Three renderings, one value. Logs want the dotted form, RFC 9457 wants the
pointer, the envelope wants the array.

### Violation and Fault

### Two origins, one list

A constraint violation (unique, foreign key, check) and a validation failure
(`go-playground/validator`, or a service-layer rule) are **the same type in the
same list**, told apart by an `Origin` field rather than kept in separate ones:

```go
type Origin uint8

const (
    OriginInput Origin = iota // the payload alone was wrong. no stored state was read.
    OriginState               // it collided with what is stored. Source is populated.
)
```

**Unify the type**, because merging is the entire point of this work. A payload
with a malformed email *and* a taken email is two violations at one path, and a
client making two round trips to learn that is the problem §1 opens with. The
envelope already assumes it: a 409 unique conflict appears under
`errors.validation` because the group describes what the client can act on, not
where the failure came from.

Ecto and Rails converged on the same thing, and it is worth stating precisely
rather than as an appeal to authority: **an error that came from the database
lands in the same collection as an input-validation error, keyed by field, and is
rendered with it.** In Rails, `validates_format_of` adds to `ActiveModel::Errors`,
and a rescued `ActiveRecord::RecordNotUnique` adds to the same place via
`errors.add(:email, :taken)` — the controller reads one `errors` and gets both. In
Ecto, `validate_format/3` and `unique_constraint/3` both append to
`changeset.errors`, one keyword list.

Two things we do **not** take from them, so §11's table is not misread. Ecto
*declares* the constraint-to-field mapping by hand; we derive it from the catalog
and keep declaration as an override, so what we borrow is the idea, not its
primacy. And Rails' `validates_uniqueness_of` is exactly the racy application-side
pre-check §11 refuses as a primary mechanism: the index is the truth and the probe
is advice.

**Separate the origin**, because three rules key off it and none has a good hook
otherwise:

- **Status.** `OriginInput` field rules are 422; `OriginState` collisions are
  409. That is §2's split, and `Origin` is the reason for it rather than a
  special case in a table.
- **The oracle.** Only `OriginState` reveals stored state, so the
  never-echo-the-value default and the code-only mode key off `Origin`
  automatically and completely — better than §8's per-constraint opt-out, which
  is only as good as whoever remembers to name a constraint.
- **Short-circuit.** If any `OriginInput` violation exists, **the probe does not
  run.** The payload is already known bad, and probing would bind values that
  had already failed validation — which §8 names as the probe's most likely
  self-inflicted error. This is free and it removes a class of wasted round
  trips.

Ordering within one path puts `OriginInput` first: a malformed value explains a
failed lookup, and the reverse reads as nonsense.

### The validation bridge, with no dependency

A converter is **required**, not optional. Our envelope is the only shape that
goes on the wire, and `validator.FieldError` is a different shape, so something
has to translate. `go-playground/validator` is also not optional in practice —
Gin's binder already runs it ([[D-034]]), so a `crudgin` consumer gets its errors
whether or not they asked for them.

What is *not* required is a **module** for that converter. [[D-033]] puts a
package in its own module when it imports an external dependency, and this one
imports nothing: `errs` declares the shape, and `validator.FieldError` satisfies
it structurally, because Go's interfaces are implicit. Neither package needs to
know the other exists. Same trick and same reason as `crudsql.sqlState` asking a
driver error for a SQLSTATE by shape ([[D-015]]):

```go
// FieldViolation is the shape a validation library's error already has.
// validator.FieldError satisfies it structurally, so neither package imports
// the other.
type FieldViolation interface {
    Namespace() string
    Tag() string
    Param() string
    Value() any
}

func FromFieldViolations[T FieldViolation](root string, vs ...T) []Violation
```

`Namespace()` is why this works. With `RegisterTagNameFunc` reading the `json`
tag, validator reports the **wire** names rather than the Go ones. Measured
against v10.30.1 with this document's own input DTO:

```
namespace=In.smth          tag=required  param=     value=
namespace=In.user.email    tag=email     param=     value=nope
namespace=In.user.age      tag=gte       param=18   value=15
```

Strip the root struct name and `In.user.email` is `["user","email"]` — the target
shape, with no reflection of ours involved. `Tag()` becomes the `Code`, `Param()`
and `Value()` become `Params`, and `Namespace()`'s `Items[3].Email` spelling
parses straight into an index `Step`. The one thing a consumer must do is register
the tag-name function; without it `Namespace()` returns Go field names and every
path is silently wrong, so that is a start-up check rather than a runtime
surprise.

The call site is one line, and the generic parameter is what buys that — Go
infers `T` as `validator.FieldError`, and `ValidationErrors` (underlying
`[]FieldError`) passes straight through as the variadic:

```go
verrs := err.(validator.ValidationErrors)
vs := errs.FromFieldViolations("CreateUserRequest", verrs...)
```

Structural satisfaction has one cost and it should be named: our interface has to
match the library's signatures exactly, and if `Namespace()` ever changes shape
the failure is not a compile error here — it is a failed type assertion at the
call site. So `errs`' test package carries
`var _ FieldViolation = (validator.FieldError)(nil)`. That assertion is the only
place in the whole design that imports the validator, and it lives in a
`_test.go`, which is what keeps [[D-033]] satisfied.

A service-layer rule produces `OriginInput` violations through the same builder,
so `{field:["user","age"], code:"too_young"}` sorts and renders beside a database
violation with no special case anywhere.

`Violation` carries no `Kind` — `Kind` is one per `Fault` — so §8's sort derives
each violation's kind from its `Code` through the wired `Codes` value. That is
also why the sort cannot be a free function.

```go
type Source struct {                 // storage-side provenance. internal only.
    Table, Schema string
    Columns       []string
    Constraint    string
}

type Violation struct {
    Path    Path
    Code    Code
    Origin  Origin                   // payload alone, or a collision with stored state
    Message string
    Params  map[string]any           // for message templating: {"max":255}
    Source  Source                   // populated only when Origin is OriginState
}

type Detail struct {                 // never rendered publicly
    Dialect    string                // "postgres", "mysql", "mariadb", "sqlite"
    SQLState   string                // "23505"
    Native     int                   // 1062, 2067
    Constraint string
    Table      string
    Columns    []string
    Value      string                // best-effort — see §6
    RefTable   string
    RefColumns []string
    Driver     error
}

type Fault struct {
    Kind       Kind
    Code       Code
    Message    string                // developer-facing. never rendered.
    Violations []Violation
    Op         string                // "Save", "Update" — the repository verb
    Entity     string
    Retryable  bool
    Partial    bool                  // a cap was hit; the set is incomplete (§8)
    Detail     Detail

    wrapped []error
}

func (f *Fault) Error() string
func (f *Fault) Unwrap() []error { return f.wrapped }
```

`Detail` and `Source` say "internal only" in a comment, and a comment is not an
enforcement. [[D-044]] is a rule the types have to carry: `Violation` and `Fault`
get their own `MarshalJSON` that emits the public shape and nothing else. Without
it the first person who logs a fault as JSON ships the constraint names this work
exists to hide — and `Detail.Driver error` makes the default marshal fail anyway.

`Unwrap() []error` is the mechanism. A fault built by the pgx adapter wraps
`crud.ErrConflict` and the `*pgconn.PgError`, so all three of these are true at
once:

```go
errors.Is(err, crud.ErrConflict)   // the transport answers 409 with no new code
errors.As(err, &fault)             // the renderer gets every violation
errors.As(err, &pgErr)             // a caller who wants the SQLSTATE still can
```

All three hold through a further `fmt.Errorf("saving user: %w", f)`, which is
what a decorator or a service layer adds — checked against Go 1.26 rather than
assumed, because the whole compatibility story rests on it.

That is [[D-015]]'s "layers wrap rather than replace", honoured exactly. The
existing switch in [http/crudhttp/errors.go](http/crudhttp/errors.go) keeps
working on the day `errs` lands, before a single renderer is written.

### The SPI — the interfaces a third party implements

```go
type Classifier  interface { Classify(err error) (*Fault, bool) }
type Resolver    interface { Resolve(Path) (Path, bool) }              // one hop of §3's chain
type CodeMapper  interface { CodeFor(*Fault, Violation) (Code, bool) }
type MessageSource interface {
    Message(ctx context.Context, v Violation, locale string) (string, bool)
}
type Renderer    interface {
    Render(ctx context.Context, err error) (status int, header http.Header, body any)
}
```

`Classifier` returns a `*Fault`, and `wrapped` is unexported — so as declared, a
third-party classifier cannot make `errors.Is(err, crud.ErrConflict)` true, which
is [[D-038]]'s entire invariant. The builder therefore exports a `Wrapping(errs
...error)` step, and that is the only way a sentinel gets attached.

`Chain(rs ...Resolver) Resolver` composes the hops. Every one of these is wired
**explicitly**, at the call site, by the consumer. There is no `init()` registry
and there will not be one:

- Multi-database makes a global registry wrong on the first day. Two databases
  can hold the same constraint name with different columns.
- A package-level registry mutated by imports makes the behaviour of a handler
  depend on which packages happen to be linked in.
- This repository already refuses singletons everywhere else, and `go.work`
  joining five modules means an `init()` in the wrong module is invisible.

Go has no `ServiceLoader` and that is fine. Explicit wiring is the idiom, and it
is also the only version that can differ per database.

### Messages

Hierarchical key lookup, taken from Spring's `MessageSource` — the one part of
Spring's error handling worth copying wholesale:

```
users.email.unique  →  users.unique  →  email.unique  →  unique  →  the code's default
```

An override can be as narrow or as broad as the author needs, with no
configuration schema to learn. Placeholders come from `Params`. The locale comes
from the context, never from the fault: a `Fault` that crosses a queue must not
carry the locale of the request that made it.

### Building one by hand

A service layer is a first-class producer of violations, not an afterthought:

```go
func (s users) Save(ctx context.Context, u *User) error {
    if u.Age < 18 {
        return errs.Validation().
            Field("Age").Code("too_young").Params(errs.P{"min": 18}).
            Fault()
    }
    return s.Repo.Save(ctx, u)
}
```

`Field("Age")` names the *model* field. The chain in §3 turns it into
`["user","age"]` on the way out. The service does not know what a JSON body looks
like and must not.

---

## 6. Dialect classification

Parsing is split in two, and the split is the same one [[D-015]] already made
for the SQLSTATE classifier:

- **Extraction** happens where a driver's type can be named.
  [`adapter/crudsql/conflict.go`](adapter/crudsql/conflict.go) asks by *shape* —
  it may not import a driver — and today reaches a `SQLState() string` method or
  an exported `SQLState` field in both its `string` and `[5]byte` spellings. It
  grows to reach the rest the same way.
  [`adapter/crudpgx`](adapter/crudpgx/conflict.go) can name `*pgconn.PgError` and
  does. Spell the fields correctly, because they differ per driver:
  `pgconn.PgError` has `Code`, `ConstraintName`, `TableName`, `SchemaName`,
  `ColumnName`, `DataTypeName` and `Detail` — there is no `Column` and no
  `Table`. `mysql.MySQLError` has `Number`, `SQLState` and `Message` and nothing
  else, so on MySQL every structural fact beyond the number comes from the
  catalog.
- **Interpretation** happens in `errs/sqlerr/`, as pure functions over
  `(dialect, sqlstate, native, message)` → `(Fault, Source)`. No driver imports,
  no database, table-driven tests, four files: `postgres.go`, `mysql.go`,
  `mariadb.go`, `sqlite.go`.

One correction to how the reach is written. `crudsql.sqlState` walks with
`errors.Unwrap` in a loop, and `errors.Unwrap` returns nil for a multi-error —
which is exactly what `fmt.Errorf("%w: %w", …)` already builds at
[conflict.go:24](adapter/crudsql/conflict.go#L24), and what `Fault.Unwrap()
[]error` will build. The richer extraction has to walk the tree the way
`errors.As` does, and `sqlState` must be fixed in the same change or it goes
blind to every fault the new code produces.

### What each engine actually tells us

**This table was measured, not remembered.** Every cell below was provoked
against a live server and is checked in under
[errs/sqlerr/testdata/corpus](errs/sqlerr/testdata/corpus): PostgreSQL 17.11,
MySQL 8.4.11, MariaDB 11.4.12, SQLite 3.53.3. MariaDB is no longer the asserted
column — the container landed with phase 0 and settled it.

Three cells came back different from what this table said when it was written
from documentation, and they are marked. One of them was a live bug.

| Class | PostgreSQL 17 | MySQL 8.4 | MariaDB 11.4 | SQLite |
|---|---|---|---|---|
| unique | `23505`; `ConstraintName`, `TableName`, `SchemaName`; `Detail` has key and value | `1062` / `23000`; index name in the message, table-prefixed since 8.0.19 | `1062` / `23000`; index name **not** table-prefixed — confirmed | ext `2067`; names `table.column`, no constraint name |
| primary key | `23505` on the pk index | `1062` on `PRIMARY` — and `PRIMARY` is the name on *every* InnoDB table | same | ext `1555`; ext `2579` for a duplicate implicit rowid |
| FK, parent missing | `23503`; `Detail`: *is not present in table "…"* | `1452` / `23000`; also `1216` when the caller lacks privileges on the parent | `1452` | ext `787`, **no detail at all** |
| FK, child referencing | `23503` — **not** `23001` | `1451` / `23000`; also `1217`. `TableName` is the **child** | `1451` | ext `787`, no detail |
| NOT NULL | `23502`; `ColumnName` and `TableName` populated | `1048` / `23000` | same | ext `1299`; names `table.column` |
| missing default | **collapses into `23502`** — PostgreSQL gives an omitted NOT NULL column the same error as an explicit NULL | **`1364` / `HY000`** — not class 23, and a distinct condition | same | **collapses into ext `1299`**, like PostgreSQL |
| CHECK | `23514`; `ConstraintName` | **`3819` / `HY000`** — not class 23 | **`4025` / `23000`** — confirmed: class 23 already covers it, so no number entry is needed | ext `275`; named → the name, **unnamed → the expression source text** |
| exclusion | `23P01`; `ConstraintName` | — | — | — |
| too long | `22001` — **no column and no table** | `1406` / `22001`; column **and** `at row N` | same | **not enforced at all** — a `VARCHAR(8)` stores 27 chars, confirmed |
| out of range | `22003` | `1264` / `22003`; column and row | same | **not enforced**: 99999 is stored as sent |
| bad syntax for type | `22P02` | `1366` / `HY000`; column and row | **`1366` / `22007`** — *not* the same. Same number, different class | **not enforced at all**: `'abc'` is stored as text in an INTEGER column |
| deadlock | `40P01` | `1213` / `40001` | same | — |
| serialisation failure | `40001` | covered by `1213` | same | ext `517` busy-snapshot — still unprovoked, see below |
| lock timeout | `55P03` | `1205` / `HY000` | same | **`5`** — the *primary* `SQLITE_BUSY`, not the ext `773` this table predicted |
| tx aborted | **`25P02`** — see §8 | n/a, statement-level rollback | n/a | n/a |

Eight consequences, and they are what the whole design turns on.

1. **SQLSTATE class is not a usable gate, and [[D-015]] was wrong about this.**
   D-015 said class 23 is *"unique key, foreign key, NOT NULL and CHECK … and
   nothing else does, so the classification needs no per-driver table."* That
   sentence is now superseded by [[D-046]], and phase 0 fixed the two live bugs
   it had caused. The evidence, in ascending order of how much it cost:

   - **MySQL** answers a CHECK violation with `3819 / HY000` and a missing
     default with `1364 / HY000`. Neither starts with `23`, so neither was
     classified: a client got a bare 500 where [[FL-011]] promises 409.
   - **MariaDB** answers the same CHECK with `4025 / 23000` — inside class 23. So
     the identical constraint on two engines sharing a driver, a dialect and a
     wire protocol needs two different arms. A number list alone is wrong about
     MariaDB; a class test alone is wrong about MySQL. D-015's forbid was right
     to leave `4025` out, and wrong about why.
   - **SQLite reports no SQLSTATE at all**, so for a quarter of the supported
     engines the gate was simply absent. **Every** SQLite constraint violation —
     unique, primary key, foreign key, NOT NULL, CHECK, all seven classes — was
     an unclassified 500, for as long as the dialect had been supported. Phase 0
     found this on the corpus's first run.

   Why nothing caught the SQLite half: `TestIntegrityViolationsAreClassifiedByEveryAdapter`
   walks `egTargets()`, and SQLite is not on that list. The dialect arrived with
   a conformance suite that exercises reads and writes, and a constraint
   violation is not part of conformance. That is the argument for the corpus in
   one sentence.
2. **SQLite's extended result codes are not interchangeable with primary ones**,
   and the corpus corrects this table's own guess about which arrives. Contending
   for a write returns the **primary** `SQLITE_BUSY` (5), not the ext `773` this
   table predicted. What is stable is the low byte: every `SQLITE_CONSTRAINT_*`
   code is `19 | (n<<8)`, so `19` is the test and the subcodes need no list —
   which is what [[D-046]]'s SQLite arm does.
3. **SQLite's foreign-key error carries nothing.** `FOREIGN KEY constraint
   failed`, and that is the whole message. Only the catalog, or
   `PRAGMA foreign_key_check`, can say which key.
4. **PostgreSQL cannot tell the two foreign-key directions apart from structured
   fields.** Both are `23503` with the same `ConstraintName`, and for the
   child-referencing direction `TableName` is the **child** — a table the request
   never mentioned. `23001` is defined but PostgreSQL does not raise it here. So
   `foreign_key` versus `restrict` is undecidable without reading localised
   `Detail` text, which rule 5 forbids: the direction must come from `Fault.Op`
   and the entity being written. And a restrict violation has no field in the
   request at all, so it belongs in the envelope's `general` group, never
   `validation`.
5. **Message text is not an interface.** PostgreSQL localises `Message`, `Detail`
   and `Hint` through `lc_messages`, and so do MySQL and MariaDB; only SQLite
   does not. **Columns come from the catalog; the offending value comes from
   `Detail` as best-effort enrichment whose failure is not an error.** Never
   depend on message text for a field path.
6. **Even the value is untrustworthy.** MySQL's `1062` joins composite key values
   with `-`, which is ambiguous the moment a value contains a hyphen — `('x-1','y')`
   reports `'x-1-y'`. On a prefix index, `UNIQUE KEY (v(5))`, the reported value
   is the *truncated prefix*, not what was sent.
7. **Three data-class violations are unreachable on SQLite**, not one. It
   enforces neither a declared width (a `VARCHAR(8)` stores 27 characters), nor a
   declared range (99999 into a small column), nor a declared type (`'abc'` into
   an integer column, kept as text). The same payloads are 422 on the two servers
   and 200 on SQLite, and the stored row then holds what the schema says it
   cannot. [[D-019]] governs it and now names all three — see §12.

8. **The same engine number can carry different classes.** MySQL's `1366` is
   `HY000` and MariaDB's is `22007`. A parser keyed on the number alone would
   agree with itself while describing two different classifications — which is
   the other half of consequence 1, and the reason the key is a triple.

Two gaps the table does not cover and the design must: **deferred constraints**
(`SET CONSTRAINTS … DEFERRED` fires `23505`/`23503` at `COMMIT`, with no
statement, no payload and no `Op`), and **`25P02`** (§8), which is already an
unclassified 500 today.
### The corpus, not the table — **built**

The matrix above is design input, and half of it was wrong the first time it was
written. Message text and error numbering drift by server version, and a table
written from memory is the wrong thing to build a classifier on.

So the **captured-message corpus** came first, ahead of the decisions rather than
after them: a checked-in fixture per engine, generated by a program that provokes
every violation class against a live server and records the driver error verbatim
along with the server version. Fifteen cases, four engines,
[errs/sqlerr/testdata/corpus](errs/sqlerr/testdata/corpus). `make corpus`
recaptures; recapturing unchanged servers is byte-identical, so a diff is always
a real change.

It was phase 0 because [[D-015]]'s class-23 sentence and the whole of §2's status
table rested on cells the corpus is the only honest source for. Writing the
decisions first would have pinned a mistake — and it would have pinned three:
consequence 1 above lists what came back different, including an entire dialect's
worth of unclassified 500s that no amount of re-reading the specification would
have surfaced.

**What is asserted and what is only recorded.** The guard compares the tuple a
classifier dispatches on — driver type, SQLSTATE, native number, and which
structured fields the driver populated. It does **not** compare the message or
the server version. [docker-compose.yml](docker-compose.yml) tracks floating tags
(`mysql:8.4`, `postgres:17-alpine`), so a patch release that rewords one sentence
would otherwise turn the suite red over a change no parser can see — and the fix
would be to stop reading the failure. Text is captured and checked in, for the
human reading a diff and for phase 2's *message text is not an interface* test,
and `make corpus` still reports every key that moved.

**The negatives, and one correction.** Three entries must stay unclassified: a
`42P01` (PostgreSQL) / `1146` (MySQL, MariaDB) undefined table, a `1044` access
denied, and a connection that never reaches a server. That last one replaces the
`08006` this section used to name: a client that cannot reach the host produces a
`*net.OpError` or a `*pgconn.ConnectError`, not a server-issued SQLSTATE.
Capturing it verbatim is the better negative — an error carrying **no SQLSTATE at
all** must stay unclassified — but the cell was wrong and is corrected rather than
faked. PostgreSQL contributes a fourth: `28P01`, a real server error in class 28.

**What is deferred to phase 2**, named here rather than dropped: `deadlock`,
`serialisation_failure`, `25P02` and deferred constraints. A deadlock needs two
goroutines racing through a barrier, and a corpus entry that depends on
scheduling regenerates differently every run. `lock_timeout` — two sequential
connections and a session variable — is deterministic and carries the retryable
class for now.

The corpus is also where the `errors.Unwrap`-versus-tree-walk fix (above) gets
its regression test.

---

## 7. The catalog

The probe cannot work without knowing the schema, and the schema cannot be a
global. One process may hold several databases ([[UC-012]]), and two of them can
disagree about what `users_email_key` means.

### Keyed on the physical handle

```go
type Catalog interface {
    Table(name string) (*Table, bool)
    Constraint(table, name string) (*Constraint, bool)
    Dialect() string
}

func Load(ctx context.Context, src crud.Source) (Catalog, error)
```

Two details that look like style and are not. `Constraint` takes the **table**
because an index name is unique per table in MySQL rather than per schema — every
InnoDB table's primary index is called `PRIMARY`, so a bare name is ambiguous
across the database. And the lookups take no `context` because a loaded catalog
does no I/O: `Load` is the thing that can fail, and it is what [[D-021]] makes
fail at start-up. A lookup signature that accepts a context is a lazy loader, and
a lazy loader cannot fail at start-up.

Keyed on the identity `crud.Identified.DataSource()` reports — the `*sql.DB`, the
`*pgxpool.Pool`, whatever the adapter holds. That is the only identity the seam
offers, and [crud/executor.go](crud/executor.go) already states the rule this
reuses: *"Two sources that answer with the same handle are the same database, and
that is the only question `WithExecutorFor` needs answered."*

Not the DSN. Two handles to the same DSN with different `search_path` are
different catalogs, and a string key would silently merge them.

Two traps the existing code already avoids and the catalog must not walk into:

- **The test is `keyOf(src) != nil`, not `src.(Identified)`.** `crud.ReadWrite`
  *is* `Identified` and answers **nil** when its primary is not. `keyOf` says
  why: *"A wrapper that forwards an identity it does not have answers nil; that
  is 'I cannot say', not 'my identity is nil'."* Testing for the interface makes
  every such source collide into one catalog entry — the silent merge the handle
  key exists to prevent.
- **Not a `map[any]`.** `sameDataSource` goes out of its way to compare
  identities without one, because *"a datasource handle is a pointer in practice,
  but nothing in the contract says it must be"* — and an uncomparable map key
  panics at run time. Catalogs live in a slice compared with that function.

A source that cannot name its database gets no catalog and degrades to
`probe.Simple`, and it says so **at declaration time**, not on the first failed
write. `crud/crudtest`'s recorder is one of these, so the probe has no unit-test
seam unless the recorder grows a `DataSource()`; §16 lists that as open.

### What it holds

Columns with nullability, default, max length, numeric precision and scale,
collation, and whether they are generated. The primary key. Unique **constraints
and unique indexes separately**, because they are not the same thing and only one
of them has a constraint name. Partial-index predicates and expression-index
expressions, verbatim. Foreign keys with referenced table and columns, `ON
DELETE` / `ON UPDATE` actions and deferrability. CHECK expressions.

Loaded through `Query` only, because `Exec` and `Query` are all the seam has:
`pg_catalog` with `pg_get_expr` / `pg_get_indexdef` / `pg_get_constraintdef` for
PostgreSQL; `information_schema` for MySQL and MariaDB, reading `STATISTICS` *and*
`TABLE_CONSTRAINTS` because a unique index that is not a constraint appears only
in the first; `PRAGMA table_xinfo` / `index_list` / `index_xinfo` /
`foreign_key_list` for SQLite, plus `sqlite_master.sql` for the things no PRAGMA
exposes. PRAGMAs return rows, so they go through `Query` like anything else.

### The scoping trap

A bare table name resolves differently per connection: PostgreSQL's
`search_path`, MySQL's current database, SQLite's `ATTACH`ed schemas. The catalog
resolves the name once, on the connection it loaded from, and **records the
resolved schema**. A later lookup by bare name against a connection with a
different `search_path` is a different table, and the catalog must not pretend
otherwise.

### When it cannot load

Insufficient privileges. A proxy that blocks `information_schema`. A dialect the
loader does not know. All three fail **loudly at start-up** ([[D-021]]): a
repository declared with `probe.Full` against a database whose catalog cannot be
read refuses to start. Degrading quietly to "no violations found" would mean the
feature is off in production and nobody knows.

### Staleness

A rolling migration adds a constraint while the process runs, and the classifier
sees a name the catalog has never heard of. The answer is a **negative cache with
backoff**: the first unknown name triggers one reload, and further unknowns are
remembered as unknown for a growing interval. Without it, a deploy that renames a
constraint turns every failed write into a full introspection pass.

### What it must not become

Not a migration tool. Not a full DDL model. Not a query planner. And above all
not a Go-side validation layer that duplicates the database's own rules —
§8 has the argument, but the short version is that two implementations of one
constraint disagree eventually, and the one in the database is the one that is
right.

---

## 8. The probe

The database reports one violation per failed statement. The probe finds the
rest.

### Two handlers, one interface

```go
package probe

type Handler interface {
    Enrich(ctx context.Context, req Request) (*errs.Fault, error)
}

func Simple() Handler                              // wrap and return. no extra query.
func Full(cat catalog.Catalog, o ...Option) Handler // one query, every violation.
```

Defaults: **`Full` for single-row `Save` and `Update`; `Simple` for `SaveAll`,
`UpdateAll`, `DeleteAll` and the bulk routes.** Swappable per resource and per
verb. A batch write is where the cost multiplies and where a client is least
likely to be a form, so the cheap handler is the default there.

`Save` needs a caveat an earlier draft of this document got wrong. `Save` **is**
the upsert path ([[D-011]]), and an upsert swallows the conflicts its own
`ON CONFLICT` target covers — so the probe skips exactly those and probes the
rest. The skipped set is **not the same on every engine**: PostgreSQL emits
`ON CONFLICT (pk) DO UPDATE`, which swallows the primary key only, while MySQL
emits `ON DUPLICATE KEY UPDATE`, which swallows *every* unique key. A payload
colliding on `users_email_key` therefore succeeds on MySQL and 409s on
PostgreSQL. That is an observable dialect difference, it exists today, and
[[D-019]] governs it — see §12. The probe derives its skip set from
`crud.Dialect`'s upsert form, never from a hard-coded rule.

### The query

One round trip, N boolean columns, one column per constraint:

```sql
SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)                      AS c0,
       EXISTS(SELECT 1 FROM users WHERE tenant_id = $2 AND slug = $3)    AS c1,
       ($4 IS NOT NULL AND NOT EXISTS(SELECT 1 FROM orgs WHERE id = $4)) AS c2
```

Five things there are load-bearing, and four are corrections to the obvious
version.

**Foreign keys need the NULL guard.** A nullable FK left NULL satisfies the
constraint — SQL's MATCH SIMPLE — so a bare `NOT EXISTS(SELECT 1 FROM orgs WHERE
id = NULL)` evaluates to `true` and the probe reports `foreign_key` on a field
that is correct. Measured against PostgreSQL 17: the probe says violation, the
insert succeeds. Composite FKs are worse — *any* NULL column disables the check
entirely, so the guard is "every column non-null", not "this one". A probe that
invents violations is strictly worse than the single-violation status quo it
replaces, which is why [[D-042]] lets the probe only ever **narrow** the
truth, never widen it.

**Results are read by column position, not by alias.** PostgreSQL truncates
identifiers at 63 bytes with a `NOTICE` no driver surfaces, so `"u:"` plus a long
constraint name silently collides with any constraint sharing its first 61
characters and the result set mis-attributes. Positional reads also make the
aliases dialect-neutral: `AS "x"` is a string *literal* in MySQL without
`ANSI_QUOTES`. Catalog order already carries the identity — [[D-014]] one layer
up — so the alias only has to be debuggable (`c0`, `c1`).

**Placeholders and quoting go through `crud.Dialect`.** `$1` is PostgreSQL-only;
MySQL and SQLite use `?`. [[D-019]] forbids a name check standing in for a
dialect check, and the probe is not exempt.

**Values come from `merge(loaded row, changes)`, not from the change set.**
`Update` is load-diff-write, so `crud.UpdatePlan` drops any field whose value
already matches the stored one — that is [[D-010]]'s invariant. For
`UNIQUE (tenant_id, slug)` where only `slug` changes, `tenant_id`'s *value* is
not in the change set; it is in the row `Update` already loaded under
`PrimaryOnly`. The change set decides **which** constraints are relevant; the
merged row supplies **what** gets bound. `Save` has no `UpdatePlan` at all — its
values come straight off the model.

**An explicit JSON `null` binds `IS NULL`, not `= NULL`.** [[D-002]]'s third
state reaches the planner as a nil value, and `WHERE email = NULL` is never true,
so a probe that binds it reports clean for a column about to fail NOT NULL.

An update also excludes its own row (`AND id <> $n`), and a bulk probe uses a
`VALUES`-derived table so each violation carries a row index.

### What needs no query at all

Intra-payload duplicates in a batch — two rows in the same insert with the same
email. The database reports one; both are wrong; finding them takes a map, not a
statement. This is the one Go-side check that is unambiguously correct, because
it is a fact about the payload rather than about the database.

Everything else is a trap, and MySQL makes the argument for us. With
`--sql-mode=STRICT_TRANS_TABLES` — which
[docker-compose.yml](docker-compose.yml) sets — a too-long value is an error.
Without it, the same value is a warning and a silent truncation. A Go-side length
check would report a violation the server would never have raised, on a
deployment the library cannot see. So: **NOT NULL, length, range and enum
membership are not checked in Go.** The database's answer is the only one that is
right, and the probe asks it.

### The transaction problem

This is the hardest part of the design and it deserves the space.

PostgreSQL aborts the entire transaction on a constraint error — SQLSTATE
`25P02`, and nothing runs until `ROLLBACK` or `ROLLBACK TO SAVEPOINT`. A savepoint
cannot be taken after the fact. MySQL and SQLite roll back the statement and
leave the transaction usable.

Two corrections to the obvious reading, both of which change the answer.

**Savepoints are already on the seam, and the probe must not hand-roll them.**
`crudsql`'s `Tx.Begin` issues `SAVEPOINT rxcrud_sp_<n>` off an atomic counter,
with `Commit` and `Rollback` as `RELEASE` and `ROLLBACK TO`; `crudpgx` gets the
same from pgx, and [[FL-009]] documents it. A probe issuing its own `SAVEPOINT`
through `Exec` bypasses that counter and can collide with a name the seam owns.
The probe calls `Beginner.Begin`.

**Ownership is not recorded, and the table below needs it.** Rows 2 and 3 differ
on *who owns the transaction*, and the seam cannot answer that: `crud.InTx` and
`crud.WithExecutorFor` both push the same `binding{ds, e, prev}` with no flag
saying "rx-crud opened this". Telling them apart needs an `owned bool` on
`binding` plus an accessor — a real, small seam change, and the roadmap should
not pretend otherwise. Detection also has to ask `ExecutorFor(ctx, src)`, not
`ExecutorFrom(ctx)`: with a foreign transaction scoped to a *different* handle,
`ExecutorFrom` says "in a transaction" while this repository's write runs outside
one. And `InTx` joins rather than nests, so row 2 is really two situations —
opened by this call, or joined from an outer one — with different savepoint
owners.

| The write ran… | PostgreSQL | MySQL / SQLite |
|---|---|---|
| outside any transaction | `Full`, no extra cost | `Full` |
| inside a transaction rx-crud opened, or joined | `Simple` by default. `Full` under an explicit `WithSavepoints()` — cost below | `Full` — statement-level rollback |
| inside a **foreign** transaction pushed in with `WithExecutor` | `Simple`. rx-crud does not own that transaction and will not take savepoints inside it | `Full` |

`WithSavepoints()` costs **two** extra statements per write, not one: a
`SAVEPOINT` before and a `RELEASE` after every success. Leaving them unreleased
is not an option — on PostgreSQL each is a subtransaction, and a transaction
holding more than 64 overflows the subxid cache, which forces pg_subtrans lookups
on **every reader in the cluster**. That is the known production cliff for
savepoint-per-statement, and it is not a round trip; it is cluster-wide read
amplification caused by one writer. So savepoint depth is capped per transaction,
with `Partial: true` beyond it.

Separately, `25P02` is a bug that exists **today** and this work should fix in
passing. After a constraint failure inside a PostgreSQL transaction every
subsequent statement returns `25P02`, which no arm classifies, so the caller gets
an opaque 500: `crud.InTx(ctx, db, …)` doing two saves where the first collides
returns a truthful 409 and then an unclassified 500. `25P02` gets a code and a
`Kind` of its own.

The foreign-transaction case is the one with no good option, and saying so is
better than inventing one. An ent or gorm transaction has its own savepoint stack
and its own expectations about what statements run inside it; issuing
`ROLLBACK TO SAVEPOINT` in the middle of somebody else's unit of work can discard
work the owner has not finished with. `Simple` is the honest answer.

**Probing on a different pooled connection is rejected outright.** It cannot see
the current transaction's uncommitted rows, so it would report a payload as clean
that the transaction itself has already made conflicting — the worst possible
failure, because it looks like a correct answer. And a replica is doubly wrong
here: the probe decides the content of a write's response, and [[D-032]] says a
read that decides a write goes to the primary.

### Pre-flight

The same machinery, run *before* the write. It gives every violation on the first
attempt with no failed statement at all, at the price of one query on the happy
path and a TOCTOU window between the check and the insert. Worth it on a signup
form; not worth it on an internal bulk importer. Chosen per endpoint, off by
default, and never a substitute for the constraint — the index is the truth and
the probe is advice.

### Caps, and being truthful about them

**A probe that fails keeps the driver's violation.** §2's "Internal first"
precedence applies to a failure of the original *classification*, not of
enrichment — otherwise the most failure-prone part of the design downgrades a
correct 409 into an opaque 500, which is the opposite of the point. And it is
genuinely failure-prone: it re-binds values from a write that already failed, so
a write rejected for a bad type re-binds the same value and fails again. Probe
error ⇒ keep what the driver said, set `Partial: true`, log. [[D-042]] says so.

Bound the constraints probed per request, the rows probed per batch, the total
probe time, and the catalog load time. The defaults have to be numbers rather
than adjectives, and §16 lists choosing them as open. A hostile client that POSTs a 10 000-row
batch must not be able to turn one failed write into a 10 000-way probe, and a
table with forty unique indexes must not either.

When a cap is hit the answer is **truthful, never silently partial**. The fault
carries `Partial: true` and the envelope says the set is incomplete, rather than
listing four violations in a way that implies there are only four. A partial
answer presented as complete is worse than the single violation we started with.

### The oracle

The probe queries rows the caller may not be allowed to see, and a
unique-violation response reveals that a value exists. `docs/usecases/Index.md`
gap 3 already records this for plain 409s; the probe multiplies it, and the
default chosen for this roadmap — `Full` on single-row creates and updates —
means every consumer inherits it unless they turn it off.

That is a deliberate choice, made with the trade in view. The controls:

- **The default message mode never echoes the offending value.** "user with this
  email already exists" and not "user with test@example.com already exists". The
  value is in `Detail` for the log, not in the payload.
- **Per-constraint opt-out.** A constraint over a column the caller cannot see is
  the dangerous case, and it is nameable.
- **Scope-aware probing** where a scope predicate is available. Note the limit
  from §3: writes carry no transport scope, so the predicate has to come from the
  `security.Policy` rather than from `WithScope`.
- **Code-only mode**, for endpoints where even the code is too much.

None of these make the oracle disappear. A unique constraint the client can
trigger is an oracle by construction, and the only complete fix is not to have a
public endpoint that writes to a globally unique column. The roadmap's job is to
make the trade visible and adjustable, not to claim it is closed.

### Determinism

Databases do not promise which constraint they report first, and a probe over a
map would not promise an order either. Violations are emitted in a stated total
order:

```
Kind precedence  →  Path lexicographically  →  Code  →  constraint name
```

Names sort before indices at the same depth; a shorter path sorts first. So the
same failing request twice produces byte-identical output — the violation-order
analogue of [[D-014]], and the thing that makes a response body testable at all.

---

## 9. Rendering and the transports

### The generic layer

`http/crudhttp` keeps its role as the half with no framework in it ([[D-034]]) and
grows the envelope, the `Kind` → status table, the 422 arm and the `Renderer`
seam. `Status` stays one switch, exported, and every binding still calls it
rather than re-deriving it.

There are three bindings now, not two: `crudnet` landed while this roadmap was
being written, and it is the useful case to design against. It imports nothing
outside the standard library, so it lives in the root module rather than one of
its own — which means its error middleware does too, and a consumer on chi or
gorilla/mux gets the rendering with no extra dependency.

### The decorators

Two shapes, because there are two things an author wants to decorate.

**The framework's own handler** already funnels through `h.opt.errorHandler`, so
this is an option:

```go
crudfiber.New(users, crudfiber.WithRenderer(myRenderer)).Routes()
```

**The author's own handlers** need middleware, and the three frameworks differ
in exactly the way [[D-034]] says a binding is allowed to differ — how a response
is written:

- Fiber handlers return an `error`, so the decorator is a wrapper — or the app's
  own `ErrorHandler`, which is the more natural seam.
- **Gin handlers return nothing.** The decorator is middleware that runs
  `c.Next()` and then renders whatever landed in `c.Errors`. It must check
  `c.Writer.Written()` first: a handler that already wrote a response gets left
  alone, because writing a second body produces a corrupt one.
- **`net/http` handlers return nothing either**, and there is no error bag to
  read. The decorator is a `func(http.Handler) http.Handler` over an
  error-returning handler type of our own, plus a `ResponseWriter` wrapper that
  records whether anything was written. `crudnet`'s own routes are ordinary
  `http.HandlerFunc`s, so the same middleware covers them and a chi or
  gorilla/mux router alike.

Both must be safe to install twice. A response rendered by the CRUD handler and
then rendered again by the middleware is the most likely way to get this wrong.
The marker lives on the response-writer wrapper the middleware already needs —
not on the `Fault`, which is a value that two goroutines may render at once and
which [[D-042]] treats as immutable. `crudgin` reads `c.Writer.Written()` for the
same reason.

### Rendering edge cases worth naming now

- **No registered message for a code** → the code's declared default, then the
  code itself. Never the driver's text.
- **A fault with zero violations** → the status, and `general` with the fault's
  own code.
- **A fault with 500 violations** → capped, with `Partial: true`. A response body
  is not a log.
- **`HEAD` and 204** → status only, no body written.
- **Headers already sent** → nothing to do but log; the status is gone.
- **A panic mid-render** → recovered by the middleware into a 500 with the silent
  body. A renderer bug must not become a dropped connection.
- **A missing translation, or a placeholder whose param is absent** → fall back
  one level up the message hierarchy rather than emitting `{max}` to a client.

### gRPC, later

`rpc/crudgrpc` maps `Kind` → `codes.Code` and renders a `google.rpc.Status` with
`BadRequest.FieldViolation` entries carrying `Path.String()`. The reason this is
a phase-9 item and not a design risk is §3: the fault is already
transport-neutral and the path chain already ends one hop before the wire. If
adding gRPC requires changing `errs`, the contract was wrong.

---

## 10. Codegen

`cmd/vv` already generates the update DTO and the typed metamodel ([[D-018]],
[[FL-010]]). It grows to generate the rest of a resource's skeleton, which is what
makes the adapter layer worth having rather than a chore:

| Generated | Why it must be generated rather than written |
|---|---|
| the transport DTOs | one per transport profile, so an HTTP body and a proto message can differ without the service noticing |
| the mapper, both directions | it is mechanical, and hand-written mappers drift from the model silently |
| **the inverse path map** | a literal `switch` from model field to input path. Generated, it is checked at build time; hand-written, it is wrong the first time somebody renames a JSON key |
| the service skeleton | embeds the default `port.Service`, so overriding one method is the whole customisation |
| the handler wiring | per binding, so mounting a resource stays one line |

A model column the DTO does not cover is a visible gap in the generated file
**and** a start-up refusal. The pattern already exists: `crud.PlanFor` builds the
update plan at `Define` time precisely "so a broken DTO fails at start-up rather
than on the first request". The inverse map is validated the same way, at the
same moment.

---

## 11. Prior art — what we take and what we refuse

Named explicitly so nobody re-litigates it.

| Take | From | Why |
|---|---|---|
| Hierarchical message keys, `entity.field.code → entity.code → field.code → code` | Spring `MessageSource` | The only scheme that lets one override be as narrow or as broad as needed, with no config schema |
| Per-dialect constraint extraction living **on the dialect** | Hibernate `ViolatedConstraintNameExtractor` | Proven the right seam: one vendor table, not one per call site |
| A field path with indices as first-class nodes | Bean Validation `Path.Node`, Zod `path`, DRF | Independently converged on by four ecosystems; it is the shape clients want |
| Vendor code tables, not only SQLSTATE classes | Spring `sql-error-codes.xml` | MySQL's 23000 collision makes SQLSTATE alone insufficient |
| **Declaring** which constraint maps to which field and message | Ecto `unique_constraint/3` | The reliable half of the mapping. Introspection is the fallback, not the mechanism |
| Structured field violations under one envelope key | DRF, Laravel, `ValidationProblemDetails` | Clients want one parser, not a shape per status |
| A stable machine code separate from the human message | gRPC `ErrorInfo.reason`, Prisma `P2002` | i18n and UI branching are different jobs |
| Retryable as its own class, not an error | gRPC `RetryInfo` | Keeps [[D-015]]'s rule that a retryable class is not a client error |

| Refuse | Why |
|---|---|
| i18n baked into the error value | The locale is a rendering concern. A fault crossing a queue must not carry the locale of the request that made it |
| Status codes hard-wired into the error | The same fault is 409 over HTTP and `ALREADY_EXISTS` over gRPC |
| Rails' `validates_uniqueness_of` as the primary mechanism | It has a documented race. The index is the truth; the probe is advice |
| Constraint names in the public payload | Exactly the leak `docs/usecases/Index.md` gap 16 records |
| `init()`-time global registration | Multi-database makes a global registry wrong on day one |
| A single-error-only API | The whole point of the work |
| Two parallel error channels, one for validation and one for constraints | Ecto and Rails both put them in one list; two lists cannot be merged, ordered or rendered as one payload, which is what a client actually needs |
| Duplicating the database's validation in Go | Two implementations of one constraint disagree eventually, and MySQL's `sql_mode` proves it |

---

## 12. What binds us

Every decision below is accepted and authoritative. Where this work needs one to
change, it says so and names the successor rather than quietly working around it.

| Decision | What it forces here |
|---|---|
| [[D-015]] errors are sentinels | A `Fault` **wraps**, never replaces. `ErrStaleVersion` keeps wrapping `ErrConflict`. A 500 says nothing. **But D-015's class-23 sentence is factually wrong** (§6) and phase 0 supersedes it: the classifier key is `(dialect, sqlstate, native)` and no arm is a prefix test |
| [[D-016]] `crud/` is stdlib-only | `crud` cannot import `errs`. The sentinel is attached by the caller |
| [[D-033]] optional deps are their own modules | **Amended** (the framework roadmap): the invariant becomes *no third-party requirement*, so `errs` gets its own module and its own version line. `catalog`, `probe`, `port` stay in the root; gRPC is a satellite |
| [[D-034]] a binding is a shell over `crudhttp` | **Superseded**, not extended. Its invariant says everything shared *must come from `crudhttp`*; moving the shared half to `port` breaks that literally, so it needs a successor rather than a hand-wave |
| [[D-022]] the handler takes an interface | The port layer is the natural next step. Type aliases keep every current signature compiling |
| [[D-008]] out of scope is 404 | The probe must not confirm a hidden row. 404 stays ahead of 403 |
| [[D-032]] a replica never decides a write | The probe runs on the primary |
| [[D-014]] SQL is deterministic | Extended to violation order |
| [[D-021]] magic fails at build or start-up | The inverse path map, the code registry and the catalog all fail at declaration time |
| [[D-002]] `Opt` has three states | The probe reads the change set, not the DTO |
| [[D-011]] `Save` is JPA-shaped | `Save` **is** the upsert path, so the probe runs but skips the constraints the `ON CONFLICT` target covers — a set that differs per dialect (§8) |
| [[D-019]] dialect differences are not observable | The work adds at least four new ones: the upsert-swallow divergence, `too_long` unreachable on SQLite, MySQL's row index for bulk attribution, and per-engine constraint skipping. D-019 forbids adding a fifth without naming it there **and in both usage guides** |
| [[D-009]] context executor capture is unconditional | The probe resolves its executor through `crud.ExecutorFor(ctx, src)`; that is what makes "never probe on another connection" enforceable rather than aspirational |
| [[D-010]] update is load-diff-write | The change set says which constraints matter; the loaded row supplies the values |

New decisions this work needs — **all nine written in phase 0**. Four govern code
that does not exist yet and say so in their status line rather than reading as
rules the tree already follows:

| id | Invariant |
|---|---|
| [[D-038]] | A fault is additive: the `crud` sentinel it wraps stays reachable with `errors.Is` |
| [[D-039]] | Message text is not an interface. Columns come from the catalog; a value parsed from a driver message is best-effort |
| [[D-040]] | A retryable class is not a client error. It gets its own `Kind` and 503, and the framework does not retry on the caller's behalf |
| [[D-041]] | The catalog is per physical handle, loaded once, never global, and its absence is a start-up failure |
| [[D-042]] | The probe is advisory. The index is the truth, and a probe that finds nothing never suppresses the driver's own violation |
| [[D-043]] | A path is translated one hop per layer, and no layer guesses a hop it does not own |
| [[D-044]] | The public payload names nothing internal — no constraint, no table, no column, no SQLSTATE, and no `Params` entry or CHECK expression derived from one |
| [[D-045]] | The shared half is transport-neutral; a binding is a shell over `port` (supersedes [[D-034]]) |
| [[D-046]] | The classifier is keyed on `(dialect, sqlstate, native)`; SQLSTATE class alone is not a gate (supersedes [[D-015]]'s class-23 sentence) |

The framework's own decisions are written and are not this document's to number:
[[D-035]] (naming), [[D-036]] (first-party requirements, amending [[D-033]]) and
[[D-037]] (`app` resolves nothing by type).

New and changed docs — the changed ones matter more, because `CLAUDE.md` makes a
stale flow a defect rather than untidiness:

- **UC-017** (new) — get every error for one payload in one response.
- **UC-015** gets a new guarantee and loses gap 16, its documented message leak.
- **UC-004** owns gap 3, so it changes too: the second half narrows, the first
  half — the gate's own unnarrowed existence probe — does not move.
- **FL-011** is the flow this work rewrites. It currently states *"`crud.ErrConflict`
  … the adapters **only**"*, which the faults decorator contradicts, and its
  failure-mode table promises 409 for a CHECK violation that MySQL delivers as an
  unclassified 500. It is also already stale on line numbers.
- **FL-013** carries the per-binding difference table a fourth transport adds to.
- **FL-009** gains the probe's savepoint use and the `owned` flag.
- **FL-014** (new) — a driver error becomes a public violation.
- **FL-015** (new) — a request through the port layer.

---

## 13. The hard problems

Collected so they are read together rather than discovered one at a time. Each is
argued in the section named.

1. **PostgreSQL's aborted transaction** (§8). Solved outside a transaction, opt-in
   inside one of ours, and honestly unsolved inside a foreign one.
2. **The enumeration oracle** (§8). Multiplied by the probe, mitigated four ways,
   closed by none of them. The default was chosen with this in view.
3. **Probe amplification as a DoS surface** (§8). Capped, and the cap is visible
   in the response.
4. **Partial and expression unique indexes** (§7, §8). A probe that ignores
   `WHERE deleted_at IS NULL` or `lower(email)` reports violations that do not
   exist. When the predicate cannot be reproduced faithfully the constraint is
   **skipped and said to be skipped** — never guessed.
5. **Message text is not an interface** (§6). `lc_messages`, MySQL's version
   drift, the ambiguous `-` join. The corpus exists because of this.
6. **Retryable classes** (§6). [[D-015]] forbids the easy answer, and the easy
   answer was wrong anyway.
7. **Multi-column unique constraints** (below).
8. **Determinism of violation order** (§8).
9. **Catalog staleness under a rolling migration** (§7).
10. **The port layer's cost** (below).

### Multi-column unique constraints

A violation of `UNIQUE (tenant_id, slug)` involves two fields. Two answers:

- **One violation at the deepest common ancestor** of the involved paths, with the
  full list in `Params`. One error, one message, and the message can say what is
  actually true — "this slug is taken in this workspace".
- **One violation per column**, which is what a form-binding UI wants, because it
  can highlight both inputs.

**Recommended: the ancestor form, with the per-column form as a policy.** The
per-column form says "slug is not unique" and "tenant_id is not unique", and
neither statement is true on its own. Correctness first; the UI can fan out from
`Params`.

### The port layer's cost

`New[M, ID, U](repo)` needs a transport-DTO type, and the obvious answer does not
compile: **Go has no default type arguments.** Adding a fourth parameter breaks
inference at every existing call site — `cannot infer In` — and a type alias
cannot supply a missing type argument either. [[D-022]] forbids breaking that
inference, so the choice is real:

- **A second constructor.** `New` keeps its three parameters and means "the body
  binds onto the model"; `NewFor[In, M, ID, U](repo, mapper)` names a distinct
  input DTO. Two entry points, not one call site touched.
- **A mapper value carrying the type**, so `In` is inferred from an argument
  rather than written.

The second constructor is the recommendation: it is the boring one, and it leaves
the zero-config case with the shorter name.

This is the one place the work touches a signature every consumer has typed. It
is worth being explicit that it is a cost, and worth doing in its own phase with
its own tests, rather than as a side effect of the error work.

---

## 14. Phases

Each phase is independently shippable, and each carries its documentation in the
same change — decision, use case, flow, and the reverse index in
[docs/flows/Index.md](docs/flows/Index.md), as `CLAUDE.md` requires. A phase is
not done because the code works.

| # | Phase | Ships | The control case that fails without it |
|---|---|---|---|
| 0 | Corpus, placeholders **and** decisions — **done** | MariaDB as a fourth engine; the captured corpus (15 cases × 4 engines) and its two live guards; D-038…D-046; the supersedes of [[D-015]] and [[D-034]]; the [[D-019]] additions; UC-017; the UC-015 and UC-004 amendments; FL-011. Two live bugs fixed on the way: MySQL's `3819`/`1364`, and every SQLite constraint violation | a corpus entry that must stay unclassified (undefined table, access denied, a connection that never reached a server) does |
| 1 | `errs/` | codes, `Kind`, `Path`, `Violation`, `Fault`, the SPI, the message source | a `Fault` wrapping a sentinel matches it — **and one wrapping none does not**. Without the negative the test passes for `errors.Join` and proves nothing |
| 2 | `errs/sqlerr/` | four parsers over the phase-0 corpus; **and the corpus entries phase 0 deferred** — `deadlock`, `serialisation_failure`, `25P02`, deferred constraints | the class is derived from `(dialect, sqlstate, native)` alone — a parser that reads message text fails a corpus entry whose text is localised |
| 3 | Driver extraction | `crudsql` by shape (and its `errors.Unwrap` walk fixed), `crudpgx` typed; **FL-014** | an unclassifiable state stays a 500. **Depends on phase 6** for `Source.Columns` on SQLite FKs and PG `22001`, which carry no column — ship it knowing those two are blank until the catalog lands |
| 4 | Render + decorators | the envelope, the 422 arm, `crudfiber` / `crudgin` / `crudnet` middleware; **[[D-043]] and [[D-044]] come into force**, closing UC-015's guarantee 11 and gap 16 | a 500 still says nothing; every route maps a refusal the same way; the precedence table, arm by arm. **`field` is approximate until phase 8** — say so in the release note rather than letting consumers parse a path that later changes |
| 5 | `port/` + adapters | commands, `Service`, `Mapper`, bindings become shells; **FL-015**; [[D-045]] comes into force and [[D-034]] becomes history | the same service mounts on all three bindings and compiles — the [[D-034]] check |
| 6 | `catalog/` | per-handle introspection on four dialects, the negative cache | an unknown constraint name does not re-introspect in a loop |
| 7 | `probe/` | `Simple`, `Full`, bulk attribution, caps, the savepoint mode, the `owned` seam flag, scope-awareness from `security.Policy` | probe off ⇒ one violation; probe on ⇒ three **distinct codes at three distinct paths** — and the negative twin, a payload with one real violation yielding exactly one, which is what catches an unguarded NULL foreign key |
| 8 | Codegen | DTOs, mapper, inverse map, service, wiring | regenerate-and-diff; a column the DTO misses refuses start-up |
| 9 | Ecosystem | `rpc/crudgrpc`, i18n catalogues, SQL Server / Oracle / CockroachDB; **FL-013**'s fourth-transport row | adding a transport requires no change to `errs`. The validator bridge is **not** here — it is dependency-free (§5) and ships with phase 1 |

### Infrastructure — **in place**

[docker-compose.yml](docker-compose.yml) runs `postgres:17-alpine`, `mysql:8.4`
and `mariadb:11.4`; `make mariadb` opens a shell and `make corpus` recaptures.
SQLite needs no container. MariaDB was the blocker and it earned its place three
times over: it settled the `4025` question, it turned up a divergence this
roadmap did not predict (`1366` is `22007` there and `HY000` on MySQL), and it
gave `crud.MySQL`'s "targets MySQL and MariaDB" its first test — 19 conformance
subtests and 110 engine-behaviour subtests, none of which had ever run.

`MODULES` gains `rpc/crudgrpc` at phase 9.

### Why FL-014 and FL-015 are not in phase 0

Both are flows, and `CLAUDE.md` is explicit that *a flow is the only place file
paths and symbols appear*. Neither's files exist yet, so writing them in phase 0
would produce two documents that fail at the one job a flow has. FL-014 lands
with phase 3 and FL-015 with phase 5, and [[FL-013]]'s pending change — a fourth
transport's row in the per-binding table — waits for phase 9 with gRPC.

The nine decisions were written in phase 0 regardless, because a decision governs
code rather than describing it. The four that govern unwritten subsystems carry
`in force from phase N` in their status line and head their evidence section
`Proven by (owed)`, so a reader can tell a rule the tree obeys from a rule the
tree owes.

---

## 15. Test strategy

[[D-020]] is the rule: tests are the specification, and **a test that would still
pass if the feature were deleted is a liability**. Every test that could pass
vacuously carries a control case next to it, in the pattern
`test/integration/gate_relscope_test.go` already uses.

| What | How it is tested | The control that fails without the feature |
|---|---|---|
| the parsers | table-driven over the phase-0 corpus | a **negative** corpus entry — `42P01`, `08006`, MySQL `1044` — that must stay unclassified. "A misclassified entry fails" is a tautology: the corpus supplies both input and expectation, so it tests the harness |
| message text is not read | table-driven | the same violation captured with `lc_messages` set to a non-English locale classifies identically. This is [[D-039]]'s actual invariant and nothing else tests it |
| driver extraction | live, all four engines, through every adapter | an unclassifiable state stays a 500 — the existing `TestOnlyIntegrityErrorsBecomeConflicts` pattern. Plus: a MySQL CHECK violation is now classified, which it is not today |
| the envelope | unit, all three bindings, triplet suites | removing an arm from the shared `Status` fails all three identically |
| the precedence table | unit | a fault mixing `Forbidden` and `Conflict` answers 403, and one mixing `Internal` with anything answers 500 with an empty body. Untested, this table is decoration |
| the 500 guard | unit, against a message built out of a SQL string, a constraint name and a connection string | the existing `TestA500NeverEchoesTheInternalError`, extended to `Detail` and `Params` |
| path resolution | unit over a generated mapper; unit over the raw-body fallback | a renamed JSON key with no mapper entry produces the *approximate* marker, not a wrong path |
| the catalog | live, all four engines | a partial index is skipped **and** a non-partial index of the same shape is not. Without the twin, a catalog that skips everything passes |
| catalog identity | unit | two `crud.ReadWrite` sources whose primaries differ do not share a catalog, and an uncomparable handle is refused at declaration rather than panicking |
| the probe, positive | live, all four engines | probe off ⇒ one violation; probe on ⇒ **three distinct codes at three distinct paths**. Counting to three passes for one violation repeated |
| the probe, negative | live, all four engines | a payload with exactly one real violation yields **exactly one** — the control that catches an unguarded NULL foreign key, a partial index replayed wrongly, or a prefix index |
| caps and truthfulness | unit + live | past the cap the answer carries `Partial: true`; a probe that errors keeps the driver's violation instead of becoming a 500 |
| the oracle controls | unit + live | with the value-echo mode on the value appears; with the default it does not |
| determinism | unit | at least eight violations spanning names, indices and equal-prefix paths, built in reverse, render byte-identically. Two violations pass by luck half the time — [[D-014]] made this argument already |
| double rendering | unit, all three bindings | a handler that already wrote a response is left alone; installing the middleware twice renders once |
| the message hierarchy | unit | each of the four levels resolves, and a missing translation falls back rather than emitting `{max}` |
| the transaction matrix | live, all four engines | inside a transaction without savepoints `Full` degrades to one violation rather than erroring; **with** `WithSavepoints()` it does not degrade; and a foreign transaction is never given a savepoint |
| MariaDB detection | live | a MariaDB CHECK violation classifies as `check`, which needs `4025` and not MySQL's `3819`. **Phase 0 measured it and the answer was not what this row assumed**: MariaDB reports `4025` with SQLSTATE `23000`, so class 23 already covers it and no number entry is needed. The divergence that does need the split is `1366` — `HY000` on MySQL, `22007` on MariaDB. Held by `TestEveryCorpusCaseClassifiesAsTheCorpusSays` |
| the corpus itself | live, all four engines | recapturing and diffing the key. Its control is that the negatives stay unclassified; without them the corpus supplies both input and expectation and only tests the harness |

Never `t.Parallel()` in `test/integration` — every test shares the same physical
tables. Errors are compared with `errors.Is` against the exported sentinels, never
by string. And the acceptance bar for every phase is the repo's own: unit green,
integration green **twice in a row**, `gofmt -l` silent, `go vet` clean on every
module.

---

## 16. Not decided yet

Left open on purpose. Each needs a decision before the phase that touches it, and
each now names the decision it feeds so the phase cannot start without noticing.


- **Whether pre-flight probing should ever be the default** for a named endpoint
  shape (a signup form), or stay entirely the application's call. Feeds
  [[D-042]], which currently says not by default.
- **Whether the framework retries a retryable class.** [[D-040]] says no, and is
  written. The argument for yes is that a serialisation failure inside a
  repository-owned transaction is the framework's own to retry and nobody else
  can see it; D-040 records that argument rather than dismissing it, and would
  have to be superseded rather than bent.
- **The cap defaults** — constraints per request, rows per batch, probe time,
  catalog load time. A cap without a number is not a cap. Feeds [[D-042]], which
  requires the truthfulness and leaves the numbers here.
- **Whether `crud/crudtest`'s recorder grows a `DataSource()`**, which is the
  difference between the probe having a unit-test seam and being
  integration-only. Feeds [[D-041]] (a source that cannot name its database gets
  no catalog) and [[D-042]] (what can be tested without a server).
- **Whether the envelope ever splits the two origins into separate groups.**
  Feeds [[UC-017]] guarantee 9, which currently says one list. The
  default is one list; a consumer whose UI treats "fix your input" and "someone
  took it" differently may want `errors.validation` and `errors.conflict`. The
  data is there either way — this is a rendering choice, and it should be made
  once rather than per endpoint.
- **Composite primary keys.** Unsupported today, and the probe's
  exclude-my-own-row clause assumes a single-column key. The probe should not be
  what forces the decision, but it will be what makes it urgent. Feeds [[D-042]].
- ~~Where a validation-library bridge lives.~~ **Settled** (§5): it is
  dependency-free, so it lives in `errs` and needs no module. The open remainder
  is whether `errs` should ship the `RegisterTagNameFunc` helper itself, which
  would mean naming the `json` tag in a package that otherwise knows nothing
  about transports.
