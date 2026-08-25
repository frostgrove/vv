# D-043 — A path is translated one hop per layer, and no layer guesses a hop it does not own

**Status:** accepted
**Invariant:** Each layer translates only the hop it already knows about. A layer that would have to guess a mapping it did not perform must not translate it; where a path cannot be resolved, it is marked approximate rather than invented.

## The decision

A database column becomes `["user","email"]` through a chain, not a lookup:

| Hop | Owner | Why it can |
|---|---|---|
| constraint / table / column → model field | the fault decorator, through `crud.Meta` | `Meta` binds a `Schema` to a table; `Schema` alone is table-independent and cached per type, so it cannot tell two databases' `users` apart |
| model field → service field path | the service | it defined its own command shape |
| service field path → transport input path | the resource adapter | it *is* the mapping — it performed it a moment ago while decoding |
| path → wire rendering | the generic transport layer | JSON array, JSON Pointer, proto field path |

The same fault renders `["user","email"]` on REST and `user.email` on gRPC, with
no per-transport error code anywhere.

## Why

**Because the alternative is a lookup table that only one layer could build, and
no layer has the information.** The repository knows columns and model fields. It
does not know the request shape — a nested `{"user":{"email":…}}` and a flat
`{"email":…}` are the same write. The handler knows the request shape and not the
column. Only the adapter knows both, and only because it just performed the
mapping in the other direction.

**Because `crud.Meta` is the boundary and `crud.Schema` is not.** `Schema` is
cached per type and table-independent, so it cannot tell two databases' `users`
apart — and a process holds several databases ([[UC-012]]). The hop belongs to
`Meta`, which is bound to a table, or it is wrong on the second datasource.

**Because a generated inverse map turns a request-time bad path into a start-up
failure.** When the adapter is generated, its inverse is generated with it, so a
column the DTO does not cover refuses to start. That is [[D-021]] applied to the
part of this design most likely to rot — a DTO and a table drift apart on an
ordinary afternoon, and the symptom would otherwise be a wrong `field` in a
production error body.

**Because the fallback has to be honest about being one.** A hand-written
endpoint has no mapper, so the generic layer walks the raw request bytes into a
leaf-path index when a fault occurs, matching on folded key name and — where the
driver supplied one — the offending value. Two limits, stated here rather than
discovered later:

- It is JSON only. Fiber's `Bind().Body()` dispatches on Content-Type and accepts
  XML and form encodings ([[D-045]], and [[D-034]] before it), and a form body
  has no nesting to index. A
  non-JSON body degrades to the model field name.
- Where the driver gives no value the index falls back to the first key folding
  to the column name and **marks the path approximate**. SQLite is the case that
  forces it: the corpus records its foreign-key error as `FOREIGN KEY constraint
  failed` and nothing else — no column, no table, no constraint.

**Because retaining the body is a copy, not a reference.** Fiber documents
`c.Body()` as *"valid only within the handler … don't store direct references"*,
and `crudfiber` builds its app with a plain `fiber.New()`, so Immutable is off. A
stored reference is a use-after-free that would surface as a corrupted field path
under load, which is the worst possible way for this to fail.

## What it forbids

- Do not let the repository or the fault decorator guess a transport path. It
  owns exactly one hop.
- Do not let a transport binding map a column to a request key. It did not do
  the decoding.
- Do not translate a hop through `crud.Schema`. Use `Meta`.
- Do not resolve a path from a driver's message text — [[D-039]].
- Do not emit a guessed path as if it were resolved. An unresolvable path is
  marked approximate, and a consumer is told in the release note that `field` is
  approximate until the generated mappers land.
- Do not hold a reference to a request body past the handler. Copy, and cap the
  copy.
- Do not add a per-transport error code so a binding can shortcut the chain.
  That is the duplication [[D-034]] and [[D-045]] both exist to prevent.

## Where it lives

The mechanism exists from phase 1; the render layer that uses it is phase 4's
and the generated mappers are phase 8's.

- `errs/violation.go:Violation.Approximate` — the marker. A path that could not
  be resolved says so rather than being invented, and the field is on the
  contract struct from the first tag because adding one afterwards is exactly
  what that tag exists to prevent.
- `errs/spi.go:Chain` — the composition. A declined hop returns the path as it
  was transformed so far, plus `false`, so the caller has something to mark.

- `crud/meta.go` — the `Meta`/`Schema` split this rests on.
- `http/crudhttp/request.go:DecodeJSON` — a pure function whose `io.ReadAll`
  result dies at return, so the carrier phase 4 owes (`DecodeJSONKeep`) has to be
  added rather than reached for.
- `repo/decorators/faults/faults.go` — the first hop, constraint and table to model field, through `crud.Meta`.

## Proven by

- Phase 1 shipped the mechanism and `TestAChainReportsWhenAHopDeclined` in
  `errs/spi_test.go` pins it — a declined hop reports false and keeps the
  earlier hops' work, with the every-hop-accepts twin as its control. The
  evidence this decision actually rests on is still owed, because nothing
  translates a path yet.
- `TestTheStepsThatWriteNothingElseReadsStillWrite` in `errs/build_test.go` pins
  `Builder.Approximate` onto the violation, with the unwritten violation as its
  control. That the marker can be set is not that anything sets it.
- Phase 4 owes: a path resolved through a generated mapper; and a renamed JSON
  key with no mapper entry produces the **approximate** marker, not a wrong path.
  Without the second, a fallback that always guessed the first matching key would
  pass.
- Phase 4 also owes the non-JSON body degrading to the model field name.
- Phase 8 owes the start-up refusal for a column the DTO does not cover.

## See also

[[D-021]] [[D-039]] [[D-044]] [[D-045]] [[UC-012]]
