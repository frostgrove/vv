# D-051 — A satellite carries one dependency *decision*, not one dependency

**Status:** accepted
**Invariant:** A published satellite module exists to isolate exactly one choice a consumer makes. The unit is the decision, not the `require` line: several requires are one decision when no consumer can take one without the others, and one require is two decisions when it drags in a second choice the consumer would have made separately.

Amends [[D-033]]'s *Where it lives*, which said "one external requirement each,
plus the library". That was true of every satellite until phase 9 and was never
the rule — it was a description of three modules that happened to need one
dependency apiece.

## The decision

`crud/rpc/crudgrpc` requires three third-party modules:

```
google.golang.org/grpc
google.golang.org/genproto/googleapis/rpc
google.golang.org/protobuf
```

That is one decision. A consumer choosing gRPC gets all three whatever this
library does: `grpc` cannot serve a request without `protobuf`'s wire types, and
`google.rpc.Status` — the thing an error travels in — is defined in `genproto`.
There is no consumer who wants two of the three, and no version of this binding
that takes fewer.

The check is a prefix, and it is one a reader can apply without running
anything: three requires under `google.golang.org/` that a consumer of any one
of them already has.

`utils/vvgoose` is the second case where lines and decisions do not count the
same. Choosing its two-line, cross-engine migration command chooses Goose, its
CLI parser, the searchable terminal selector and the drivers the command
registers. Removing any of those changes the consumer decision: application
`main` would grow driver imports, ambiguity would stop being interactive, or
the command would stop accepting the same four-engine config. [[D-064]] records
the exact boundary. A lower-level Goose adapter without the CLI or bundled
drivers would be another decision and does not belong in this satellite.

## Why

**Because the rule [[D-033]] is actually enforcing is "a consumer downloads only
what it chose".** A count of requires is a proxy for that, and phase 9 is where
the proxy and the thing it stands for come apart. Splitting `crudgrpc` into
three modules to satisfy a literal reading would give a consumer three `go get`
lines for one decision — the opposite of what D-033 is for.

**Because the failure the count was guarding against is a different shape.** The
one D-033 names is a satellite quietly becoming a distribution: a binding that
requires a framework *and* a metrics library *and* a tracer, so a consumer who
wanted the binding takes three unrelated choices with it. The test for that is
not how many requires there are. It is whether a consumer could plausibly have
wanted one and not another.

**Because `ROADMAP-framework.md` §9 already argued this and no decision recorded
it.** Its second half is the important one and belongs here: a module that
genuinely needs two decisions — OpenTelemetry *and* Gin — is not a binding. It
is an instrumentation of a seam, and it belongs where the seam is, taking one
dependency, with the framework reached through the interface the binding already
exports. A satellite that would need two decisions is a design error, and the
answer is to find the seam rather than to publish the pair.

## What it forbids

- Do not add a require to a satellite that a consumer of its named dependency
  would not already have. A logging library, a metrics client, a tracer or a
  second serialization format is a second decision and belongs in a module of
  its own — or, more usually, nowhere.
- Do not split a module to make the count come out at one. `crudgrpc` is not
  three modules.
- Do not read a satellite's *indirect* requires as decisions. They are the
  dependency's own graph and a consumer never chose them; `check-deps` counts
  them (113 packages for `crudgrpc`) and that number is information, not a
  budget.
- Do not use this to relax the root module. The root still has **no** third-party
  requirement at all ([[D-036]]), and `make check-deps` is what says so.

## Where it lives

- `crud/rpc/crudgrpc/go.mod` — the three requires, with the decision written at the
  top of the file where a reader meets it first.
- `utils/vvgoose/go.mod` — the migration-command decision and the dependencies
  required to make its one-call entrypoint complete.
- `scripts/common.sh:satellites` and `scripts/checks.sh:check_deps` — the per-module external
  package count, which reports rather than caps.
- `ROADMAP-framework.md` §9 — the argument this decision records, including the
  otel/gin case it turns down.

## Proven by

- `make check-deps` — the root module lists zero non-standard packages and
  `crud/rpc/crudgrpc` lists 113. The first is the assertion; the second is what makes
  it meaningful, because a satellite that isolated nothing would report zero too.
- `make check-tidy` — every `go.mod` matches its own imports, so a require that
  is not actually needed cannot sit in a satellite unnoticed.
- The whole of `crud/rpc/crudgrpc` compiles against exactly those three, which is what
  makes "one decision" checkable rather than asserted: adding a fourth
  third-party import fails `check-tidy` until somebody writes the require, and
  writing it is the moment this decision applies.

## See also

[[D-033]] [[D-036]] [[D-052]] [[D-048]]
