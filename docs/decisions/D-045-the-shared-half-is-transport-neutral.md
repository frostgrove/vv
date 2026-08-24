# D-045 — The shared half is transport-neutral; a binding is a shell over `port`

**Status:** accepted — in force from phase 5 (`ROADMAP-errors.md` §14)
**Invariant:** Everything a transport binding does that is not routing, decoding or writing a response comes from a transport-neutral package. Nothing shared may be shaped by HTTP, and no binding — HTTP or otherwise — may re-derive the status table, the code mapping or the field clearing.

Supersedes [[D-034]], which is right about the rule and wrong about the address.
D-034 stays in force until phase 5 moves the shared half.

## The decision

D-034 says everything shared *must come from `http/crudhttp`*. That was true
while every binding was HTTP. A gRPC binding breaks it literally: gRPC cannot
implement a renderer returning `(status int, header http.Header, body any)`, so
either gRPC re-derives the mapping — the exact duplication D-034 exists to
prevent — or the shared half moves somewhere with no `net/http` in it.

It moves to `port`. `http/crudhttp` keeps what is genuinely HTTP: the status
table, the response body, the header. `port` holds what is not: the commands,
the `Service` interface, the `Mapper`, the code vocabulary.

This is a narrowing of D-034's address, not a relaxation of its rule. A binding
still owns exactly three things — which routes exist, how a body becomes a Go
value, and how a response is written.

## Why

**Because D-034's own argument generalises further than D-034 did.** It was
written when the third binding was added and the measurement was that two
bindings had already drifted. A fourth binding on a different protocol is the
same measurement with a longer lever.

**Because "transport-neutral" has a test and `crudhttp` fails it.** The test is:
can a non-HTTP transport implement this interface without importing `net/http`?
A renderer returning an `http.Header` cannot be implemented by gRPC, so it is
not the shared half — it is the HTTP half wearing a neutral name. That is why
the renderer seam stays in `http/crudhttp` and does not migrate to `errs`, and
it is worth saying because the obvious place to put a renderer is next to the
errors it renders.

**Because type aliases make it a move rather than a break.** `crudfiber.Repository`
and `crudfiber.ErrorBody` are already aliases; re-pointing an alias changes no
consumer's code. The same trick that let D-034 land without a breaking change
lets its successor land the same way.

**Because [[D-022]] already pointed here.** The handler takes an interface, not a
concrete repository, precisely so a service can sit in between. The port layer is
that seam made explicit, and D-022's type aliases keep every current signature
compiling.

**What it costs.** A layer with a real price: one more indirection, one more
generated artefact, and a hand-written service now has two shapes to satisfy
instead of one. `ROADMAP-errors.md` §13 names it as a hard problem rather than a
free win, and this decision does not pretend otherwise. The price buys a second
protocol without a second status table.

## What it forbids

- Do not put an HTTP type in the shared half. If gRPC cannot implement it, it is
  not shared.
- Do not re-derive the status table, the code mapping, the bad-request sentinel
  or the create-time field clearing in a binding. That is D-034's forbid and it
  survives verbatim.
- Do not give a binding its own error codes so it can skip the chain — see
  [[D-043]].
- Do not move the renderer out of `http/crudhttp`. It is HTTP-shaped on purpose.
- Do not break a binding's exported surface while moving. Alias, as D-034 did.

## Where it lives

Nothing yet. `port/TODO.md` holds the place; phase 5 creates it.

- `http/crudhttp/` — what stays, and what the move has to leave behind.
- `http/crudfiber/`, `http/crudgin/`, `http/crudnet/` — the three shells, each of
  which must still compile against the same service after the move.

## Proven by (owed)

- Phase 5 owes the [[D-034]] check in its new form: the same service mounts on
  all three bindings and compiles.
- And the triplet suites that already exist keep their meaning — removing an arm
  from the shared status table fails all three bindings identically.
- Phase 9 owes the real test: adding a transport requires no change to `errs`.

## See also

[[D-034]] [[D-022]] [[D-015]] [[D-043]] [[D-033]]
