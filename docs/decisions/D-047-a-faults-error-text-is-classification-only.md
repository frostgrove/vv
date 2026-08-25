# D-047 — A fault's `Error()` text is classification only

**Status:** accepted
**Invariant:** `Fault.Error()` names the kind, the code, the op, the entity and the number of violations. It never carries the developer `Message`, the `Detail`, a `Source`, a `Params` entry, or one word from any error the fault wraps.

## The decision

A Go error's `Error()` conventionally reads like a sentence built out of
everything underneath it — `saving user: exec: pq: duplicate key value violates
unique constraint "users_email_key"`. A `Fault` deliberately does not.

It says what class of failure this was and how many things were wrong. Anything
richer is reached through the exported fields, through `errors.As` to the driver
error, or through `Fault.MarshalJSON`, which emits the public projection.

## Why

**Because `Error()` was already wired to a response body.**
`crudhttp`'s old `Body` copied the *outermost* `err.Error()` into the body
of every status below 500, and phase 4 replaced that body with the envelope —
`port.FaultOf` reads no error text at all now. **Phase 3 made this live rather
than prospective.** A
classified 409 now reads `{"error":"conflict","message":"errs: conflict: unique
(1 violation)"}`; an unclassified one — an unlisted class-23 number, a joined
foreign transaction, a source that named no engine — still carries the driver's
sentence, constraint name and all. A constraint name in `Error()` would be a live
disclosure three phases before [[D-044]] comes into force, and phase 4 removes
the *path* rather than the disclosure: `Error()` still reaches a log line, a
trace attribute and somebody's `fmt.Errorf`.

It is also why the fault is the *outermost* error the adapter returns rather than
something hung underneath a `fmt.Errorf("%w: %w", …)`. Underneath, `Body` would
copy the wrapper's text, this rule would buy nothing, and every test proving the
fault's text clean would be measuring a value no client reads.

**Because the convention would be restored by someone acting reasonably.**
Dropping the wrapped errors' text from a Go error looks like an oversight. It is
a one-line "fix", it makes every log line richer, and nothing in the type says
why not. That is exactly the shape of change this directory exists to catch.

**Because the debug channel already exists and is better.** `Detail`, `Source`,
`Violations` and `Params` are exported. An operator who wants the constraint
name reaches for `errs.AsFault(err)` or `errors.As(err, &pgErr)` and gets a
structured value rather than a string somebody has to parse. Nothing is lost by
keeping it out of one method.

**What it does carry, and why that is safe.** `Op` and `Entity` are the
repository verb and the model name — the caller's own vocabulary, not the
schema's. A client that called `POST /users` learns nothing from `Save
User`. `Kind` and `Code` are the two things the design intends a client to
branch on. The violation *count* is a number, not the violations.

## What it forbids

- Do not add the wrapped errors, `Detail`, `Source`, `Message` or `Params` to
  `Fault.Error()`, and do not make `Fault.String()` differ from it.
- Do not use `Error()` as the debug channel. The exported fields and
  `MarshalJSON` are that.
- Do not relax this once phase 4 lands. The envelope replacing `Body` removes
  one path from `Error()` to a client, not every path — a log shipper, a trace
  attribute and a `fmt.Errorf` in somebody's handler are the others.
- Do not add anything to `Error()` that a classifier learned from a driver. If
  it came out of an engine, it is `Detail`'s.

## Where it lives

- `errs/fault.go:Fault.Error` — the method.
- `sqlfault/classify.go:Classifier.Classify` — the producer, and the one place a
  classifier could put driver text into a fault. It carries the driver error in
  `Detail.Driver` and the constraint in `Detail`/`Source`, and writes neither
  into anything `Error()` prints. It is also where the sentinel is put *inside*
  the fault, which is what keeps the fault outermost.
- `errs/fault.go:Fault.String` — the value-receiver twin. Without it,
  `fmt.Sprintf("%+v", *f)` prints the whole struct, `Detail` included, because a
  `Fault` value does not satisfy `error` and fmt falls through to the struct
  printer.
- `errs/violation.go:Violation.String` — the same rule one level down.
- `http/crudhttp/render.go:EnvelopeRenderer.Render` and `port/kind.go:FaultOf` —
  what replaced the body that copied `Error()`. The rule survives its own reason:
  a fault's text still reaches a log, a `%w` chain and an operator's screen.

## Proven by

- `TestAClassifiedConflictIsItsOwnOutermostError` in
  `sqlfault/classify_test.go` — the other half of the sentence above: the fault
  is what the adapter returns, so `Body` copies *its* text. Hung underneath a
  `fmt.Errorf`, the 409 body would carry the wrapper's. Its control is the
  unclassified path beside it, whose outermost text is still the driver's.
- `TestAFaultCarriesNothingTheDriverSaidInItsErrorText` in
  `sqlfault/classify_test.go` — the same claim at the producer, over a real
  classification: a driver error whose message holds a SQL statement, a
  constraint name and a connection string and whose fields hold a constraint, a
  table, a schema and a column. Same two controls, and the native number gets its
  own guard on a MySQL fixture whose number is 1062 rather than PostgreSQL's
  zero.
- `TestAClassifiedConflictsBodyCarriesNothingInternal` in
  `http/crudnet/write_edge_test.go`, with identical twins in `http/crudfiber/`
  and `http/crudgin/` — the rule under live load, through the route. Its second
  control is the one that matters: the *unclassified* conflict beside it is
  asserted to still carry the constraint name in its body, so if something else
  ever closes that leak this fails and says the positive has stopped proving
  anything.
- `TestAFaultsErrorTextCarriesNothingInternal` in `errs/fault_test.go` — a fault
  whose `Detail` holds a SQLSTATE, a native number, a constraint, a table, a
  column, the offending value and a driver error whose message is a SQL
  statement plus a connection string, and whose developer `Message` names the
  constraint, produces an `Error()` containing none of them. It has two
  controls: every string searched for is asserted non-empty on the fixture
  first, or the test would pass for an empty fault; and `Error()` is asserted to
  still name the op, the entity, the kind, the code and the violation count, or
  it would pass for a method that returned `""`. The native number gets its own
  guard rather than joining the non-empty loop: it is an `int`, so a zero would
  be searched for as `"0"` and matched by any digit `Error()` prints — the
  violation count among them.
- `TestAFaultsErrorTextNamesWhicheverOfOpAndEntityItWas` in the same file — the
  four prefix arms as exact text: `errs: Save User: conflict`, `errs: Save:
  conflict`, `errs: User: conflict`, `errs: conflict`. Three of them are
  unreachable from the test above, which sets both fields, and each could be
  deleted on its own with the suite green.

## See also

[[D-015]] [[D-044]] [[D-038]] [[FL-011]]
