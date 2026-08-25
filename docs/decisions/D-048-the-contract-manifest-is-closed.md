# D-048 — The contract manifest is closed until a second implementation asks

**Status:** accepted
**Invariant:** A package joins the contract manifest — `crud`, `query`, `errs`, `port` — only when a **second** implementation of it exists or is being written, and never when the standard library or an established ecosystem standard already contracts the thing. A package with one implementation is an implementation, not a contract.

## The decision

`Makefile:TIER0` is the manifest, and `make check-tiers` is what makes it real
rather than aspirational. It holds four names and this decision closes it:
nothing on the framework roadmap's `?` list joins.

| candidate | verdict | why |
|---|---|---|
| `log` | **refused** | `slog.Handler` *is* the seam. A facade in front of it is what every pre-1.21 Go logging facade tried and lost |
| `i18n` | **refused as a subsystem**, and phase 9 demonstrated it | `errs.MessageSource` already is it, at the right size. Phase 9 needed catalogues on disk and they are `errs/catalogue.go` — one file, `io/fs` and `encoding/json`, no package and no manifest entry. If a second subsystem ever wants it, that is a move, not a design — and stdlib-only i18n cannot reach CLDR plural rules anyway, because `golang.org/x/text` is external, so it would be a satellite the day it existed |
| `health` | **refused** | three method signatures and zero implementations. A health endpoint is application code |
| `migrate` | **refused** | zero implementations, and [[D-041]] already forbids the one package that could drift into it — *"not a migration tool, not a full DDL model"*. A migration tool is a product |
| `catalog` | **refused; it is the package that row is about** | it shipped in phase 6 with one implementation and four back-ends, which is one implementation of a reader, not a contract a third party writes against. `Catalog` is an interface because the four engines answer it four ways, and that is polymorphism inside a package rather than a manifest entry |
| `app` | **refused, and already answered** | a composition root has exactly one implementation by construction. What survives is `app.Run(ctx, ...func(context.Context) error)`; [[D-037]] is what keeps it from becoming a container |
| `portkafka` | **refused, and misnamed** | it fails the second-implementation rule, and the name violates [[D-035]]: a prefix names the *subsystem*, and `port` is a layer. A Kafka binding for CRUD would be `crudkafka` |
| `obsotel` | **refused as a contract** | tracing's contract is OpenTelemetry's own, which is rule 2 applied to the ecosystem rather than the standard library. An `obsotel` satellite could exist later as an *implementation* |
| `authjwt` | **refused** | `security.Policy` is the seam and it already exists. JWT parsing is application code with an external dependency — a satellite at most, never a contract |

## Why

**Because the failure mode is a contract with exactly one implementation,
forever.** go-kit shipped contracts for everything, nobody substituted most of
them, and it ended at v0.13.0 with 29 direct dependencies. Every unused
indirection is a cost with no payer: a reader has to follow it, a change has to
go through it, and nothing is ever swapped behind it.

**Because the two rules have already been tested against this repository and
they discriminate.** `crud` has four adapters. `errs.Classifier` will have four
dialects. `query` has two doors — JSON and query string — that must agree.
`port` has three HTTP transports and, since phase 9, a fourth on another
protocol — `rpc/crudgrpc`, which is the one that proved the point rather than
restating it. Those earn it. Nothing on the `?` list has two of anything.

**Because the draft's own heuristic got it backwards.** It flagged `codegen`,
which should never have a contract, and passed `config`, which had no
implementation at all. The rule that replaces it is not a judgement about
importance; it is a count.

**Why refuse `i18n` when the errors roadmap needs messages.** Because it does not
need a *subsystem* — it needs `errs.MessageSource`, one interface, in the package
that raises the messages. The moment a second subsystem wants translations, the
interface moves and the manifest gains a name. That is a five-line change, and
doing it now would be paying for it years early.

Phase 9 is where that stopped being an argument. It shipped what a consumer
actually asked for — a catalogue read from files, one per locale — as
`errs.LoadMessages` and `Messages.Load` in `errs/catalogue.go`: stdlib only,
inside the package that raises the messages, no new name anywhere. The manifest
did not move.

**And `crudgrpc` does not join it either.** It is an implementation of a
transport, not a contract: what a third party writes against is `port.Service`,
which is already on the manifest. A fourth binding is the count rule's evidence
for `port`, not a candidate of its own.

**What this does not forbid.** A package may exist without being on the manifest.
`vvflag`, `tools/vvcfg`, `internal/codegen` and `app` are all implementations
with no contract at all, and that is the normal case rather than the exception.
The manifest is the short list of things a third party writes *against*.

## What it forbids

- Do not add a name to `Makefile:TIER0` without a second implementation that
  exists or is being written in the same change.
- Do not define a contract for something the standard library contracts. `slog`,
  `io`, `context`, `database/sql/driver`, `net/http` — a facade over any of them
  is refused by name.
- Do not define a contract for something an established ecosystem standard
  already contracts, where the standard is what a consumer would substitute
  anyway. OpenTelemetry is the case that matters.
- Do not name a package `port<library>`. The prefix names the subsystem
  ([[D-035]]), and `port` is a layer.
- Do not read this as forbidding the package. It forbids the *contract*.

## Where it lives

- `Makefile:TIER0` — the manifest, four names.
- `Makefile:TIER0_STDLIB` / `Makefile:TIER0_SEALED` — the two tighter arms.
  `crud` may import only the standard library ([[D-016]]'s surviving half);
  `errs` may import only the standard library and `errs/...`, checked with
  `-test` because a test-only import becomes a require in the module `errs` is
  split into ([[D-036]]).
- `Makefile:check-tiers` — what enforces all three.
- `ROADMAP-framework.md` §2 (the two rules), §3 (the `?` list this closes),
  §5 (`app`), §12.

## Proven by

```
make check-tiers
```

fails when a contract package imports outside the manifest. All three arms were
verified by breaking them: `errs` importing `crud` fails the sealed arm, the
same import from a `_test.go` alone fails it too, and `crud` importing `errs`
fails the stdlib arm. An import nothing can resolve also fails, rather than
reporting ok on a listing that never happened.

Nothing tests the *count* rule; it is a review rule, and the manifest is four
lines in a Makefile so that a change to it is visible in a diff rather than
buried in a package list.

## See also

[[D-033]] [[D-035]] [[D-036]] [[D-037]] [[D-041]] [[D-016]]
