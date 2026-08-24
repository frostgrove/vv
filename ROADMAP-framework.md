# Roadmap — the framework

**Status:** proposed. The rename to `ordo` is decided and not yet executed; the
structure below is a target, not a description of the tree today.
**Scope:** what `github.com/shardit-io/qq` is, how its modules are drawn, what
enforces the boundaries, and what has to be settled before the first tag.

This is one of two roadmaps and they are deliberately separate:

| | this document | [the error subsystem](ROADMAP-errors.md) |
|---|---|---|
| answers | where does code live, and what may it import | what does a failed write tell the client |
| changes when | a subsystem is added or a module boundary moves | the error contract or the probe changes |
| deadline | **the first tag** — module paths and names stop being free | none; it is phased work |

Read this one first. Every package the error roadmap proposes lands somewhere in
the structure below, and the module decisions here are the ones that expire.

---

The repository was `go-rx-crud`, is `github.com/shardit-io/qq` on disk today, and
is becoming **`github.com/shardit-io/qq`** — Latin for order, rank, and an
official register, which is both halves of what this library does. §11 records why
and what it costs; this document describes the target.

That is not a cosmetic rename: it changes what the repository is for. Today every
package in it is CRUD. The target is a framework in which CRUD is **one subsystem
among several**, and this document is the shape of that.

Three reviews took it apart — one on module mechanics, one on design,
one comparing it against shipped Go ecosystems. Most of what follows was rewritten
because of them, and the parts that were wrong are named rather than quietly
corrected.

---

## 1. What is forced, and what is a proposal

An earlier draft claimed the layout was "forced, not chosen." That was false, and
the document contradicted itself two hundred lines later by filing four of its
eight tier-0 members under §12, "not decided yet." What is actually forced is
narrow:

- **[[D-033]]** — the published root module has no external requirement; a package
  that imports one becomes a module under the same repository; releases are
  lockstep with directory-prefixed tags.
- **[[D-016]]**'s surviving half — no file in `crud/` outside `_test.go` imports
  anything but the standard library.

That is a rule about **dependencies**. Everything else below — which subsystems
exist, which get a contract, what they are called — is a proposal that has to earn
each package. Saying otherwise dressed up choices as consequences.

---

## 2. One tier boundary the toolchain can see, and one manifest it cannot

The draft had four tiers and claimed the tier was "decided by what the package
imports." Measured, that is not true. `go list` says `query` imports `crud`, so
tier 0 is not "stdlib only"; and tier 1's rule was "stdlib only, **but they do
something**", which is a judgement, not an import graph. `crud` itself is 4,610
non-test lines containing a SQL writer (`render.go`), `unsafe` offset arithmetic
(`access.go`) and reflective schema building (`meta.go`) — closer to Hibernate's
internals than to `javax.persistence`.

There is exactly **one** boundary the toolchain enforces, and it is the module
boundary:

| | Rule | Where | Cost to a consumer |
|---|---|---|---|
| **Root module** | no third-party dependencies; `errs` is the one first-party require | `crud`, `query`, `port`, `repo/*`, `catalog`, `probe`, `adapter/crudsql`, `http/crudhttp`, `http/crudnet`, `ordoflag`, `cmd/ordo` | nothing beyond `errs`. Measured today: a root-module consumer resolves **2 modules and no `go.sum` at all** |
| **Satellite modules** | one dependency decision each | `http/crudfiber`, `http/crudgin`, `adapter/crudpgx`, `rpc/crudgrpc` | only what you `go get`. Measured: a `crudgin` consumer resolves 58 modules, with fiber and pgx absent |
| **Unpublished** | needs the world | `test/`, `_examples/` | never reaches a consumer |

**Contracts are a separate, declared list** — a manifest, not a derivation, and it
should be written down as one:

```
crud    query    errs    port
```

`errs` is on that list and in a module of its own — a contract with the widest
intended audience and the least reason to share a release cadence with anything
else.

Two rules decide what joins it, and both replace the draft's heuristic:

1. **A contract is earned by the second implementation, not the first.** `crud`
   has four adapters; `errs.Classifier` will have four dialects. `config`, `log`,
   `health` have zero or one, which is indirection with a cost and no payer.
2. **Never define a contract for something the standard library already
   contracts.** That rule alone deletes `log`: `slog.Handler` *is* the seam, and a
   facade in front of it is what every pre-1.21 Go logging facade tried and lost.

The draft's stated failure mode — *"a subsystem without a tier-0 contract"* —
was backwards. Applied to its own table it flagged `codegen`, which should never
have a contract, and passed `config`, which had no implementation at all. The real
failure mode is **a contract with exactly one implementation, forever**: go-kit
shipped contracts for everything, nobody substituted most of them, and its last
release was v0.13.0 in May 2023 with 29 direct dependencies and an open issue
titled "Abandoned packages."

---

## 3. The framework as it stands

`✓` exists · `→` designed in this document · `?` proposed, and must clear the bar
in §12 before it is more than a name

```
SEPARATE MODULE — the error contract, on its own version line
  → errs/                       codes, Kind, Path, Violation, Fault, the SPI
  → errs/sqlerr/                the four dialect parsers

ROOT MODULE — no third-party dependencies
  ✓ crud/                       the datasource seam, metadata, predicates, Opt, pagination
  ✓ query/                      the wire query DSL
  → port/                       transport-neutral commands, Service, Mapper, the path chain
  ✓ repo/basic/                 the repository that speaks SQL
  ✓ repo/decorators/specs/      Specifications, Criteria, metamodel
  ✓ repo/decorators/security/   row scope, authorization, per-entity checks
  → repo/decorators/faults/     integrity errors become rich Faults
  → catalog/                    per-database schema introspection
  → probe/                      Simple and Full violation handlers
  ✓ adapter/crudsql/            database/sql — and so ent, gorm, sqlx, sqlc, bun
  ✓ http/crudhttp/              the framework-free half of the HTTP layer
  ✓ http/crudnet/               the net/http binding
  ✓ crud/crudtest/              an in-memory source for unit tests
  ✓ cmd/rxcrud/ → cmd/ordo/     the CLI, generalised past codegen

SATELLITE MODULES — one dependency decision each
  ✓ http/crudfiber/   + fiber/v3
  ✓ http/crudgin/     + gin
  ✓ adapter/crudpgx/  + pgx/v5
  → rpc/crudgrpc/     + grpc, genproto, protobuf   (one decision, three requires)

UNPUBLISHED
  ✓ test/         the integration suite: ent, gorm, sqlx, sqlc, four engines
  ✓ _examples/    every stack the guides demonstrate

ADOPTED FROM `old-rx` — see below
  → ordoflag/         generic flag parsing. stdlib only, so root module.
  → tools/ordocfg/    config loading over cleanenv. one decision, so a satellite.

?  log, i18n, health, migrate, app, portkafka, obsotel, authjwt   — see §12
```

`i18n` is not a subsystem: `errs.MessageSource` (the errors roadmap §5) already is it, at the right
size, and stdlib-only i18n cannot reach CLDR plural rules anyway because
`golang.org/x/text` is an external dependency. `health` is three method
signatures. Both were packages invented to fill a column.

---

## 4. What comes from `old-rx`, and what configuration actually is

`../old-rx` (module `github.com/shardit-io/go-rx`) already holds two working
packages, and they settle the argument the reviews had about `config` — by
showing it was the wrong argument.

The reviews said a tier-0 `config` contract is meaningless: precedence is the
only thing a configuration library has an opinion about, an interface cannot
express precedence, and a stdlib-only package cannot read YAML at all. All true.
But the conclusion is not "no configuration subsystem." It is **that
configuration is an implementation and never a contract**, which is exactly what
`old-rx` already built:

| From `old-rx` | Lines | Dependencies | Lands in |
|---|---|---|---|
| `v0/native-utils/rxflag` — `Parse[T](name, default) (T, bool)` | 171 | **none** — `os`, `strconv`, `strings` | root module as `ordoflag/` |
| `v0/tools/rxcfg` — `MustLoad[T](path) *T` over `cleanenv` | 54 | `cleanenv` (→ TOML, YAML, env, edn) | a satellite, `tools/ordocfg/` |

That split is the "one dependency decision" rule doing its job rather than a tier
being asserted: `ordoflag` costs a consumer nothing, `ordocfg` costs them cleanenv,
and **neither needs an interface** because nobody will implement flag parsing
twice. It also dissolves the YAML objection — the format support comes from
cleanenv, in the module that already declared that dependency.

Bringing them to framework level is small and mostly means fixing things that are
wrong now. Named, because "port it over" is not a plan:

- **`rxflag.Parse` cannot tell "absent" from "malformed."** Both return
  `(zero, false)`, so `--port=abc` silently becomes the zero value. That is a
  request-time failure from a start-up mistake, which is [[D-021]] inverted. It
  needs `(T, error)` or a three-state, and the caller has to be able to refuse to
  start.
- **It reads `os.Args` directly**, so it cannot be tested without mutating global
  state. Take `[]string`; keep an `os.Args` wrapper for the call site.
- **A named type is unsupported.** The type switch is on `any(defaultValue).(type)`,
  so `type Port int` falls to `default` and returns `(zero, false)` — silently,
  again. Switch on the reflect kind, or accept a parse function.
- **`--name value` mis-parses negatives**: the next-argument form is guarded by
  `!strings.HasPrefix(next, "-")`, so `--port -1` is not `-1`. No `--` terminator,
  no repeated flags, no unknown-flag detection either.
- **`rxcfg.MustLoad[T](configPath ...string)` ignores its own argument** — it
  calls `fetchConfigPath()` regardless. A live bug, and the variadic signature
  advertises the opposite.
- **It prints to stdout.** `fmt.Println("Cfg path", ...)` in a library fights the
  application's logging, which is the same reason §12's `log` entry says the
  framework should not log at all.
- **It computes `absolutePath` and then stats the original path**, using the
  absolute one only in that print. Dead code with a misleading name.
- **`Load` returning an error must exist**, with `MustLoad` as the thin wrapper.
  Panicking is the right default for a start-up path and the wrong only option.
- **The validation hook is the point of the whole package** and is missing: decode,
  then `Validate() error`, then refuse to start. That is what makes configuration
  a [[D-021]] subject rather than a file reader.

The precedence it already implements — `--config-path`, then `CONFIG_PATH`, then refuse — is
good and should be kept and written down, because precedence is the one thing
consumers will depend on.

---

## 5. `app/`, and why [[D-021]] does not protect it

The draft put `app/` in tier 1 as "the composition root: wiring, lifecycle,
shutdown," and defended it with *"not a dependency-injection container."* That is
an intention with no mechanism, and the review traced the three steps by which it
becomes one anyway: a component list, then start ordering, then resolving a
dependency two components share — which is a `map[reflect.Type]any`.

**[[D-021]] is the licence for that, not the guard against it.** Its invariant is
that the magic must fail at build or start-up rather than at request time, and a
container built and validated in `Run()` satisfies it completely. Compare the errors roadmap §5,
which refuses the analogous risk with a mechanism and three named reasons: *"There
is no `init()` registry and there will not be one."* `app/` had no such sentence.

So it gets one, and it is a decision rather than a paragraph: **no component is
ever resolved by type; every dependency is passed as a Go value at a call site the
consumer wrote; `app` never holds a `map[reflect.Type]any`.** What survives is
`app.Run(ctx, ...func(context.Context) error)` — signal handling, ordered
shutdown, the forty lines every Go service copies. Not a subsystem.

---

## 6. Naming: `<subsystem><library>`, and a prefix only where something collides

The mechanical half is settled and is the ecosystem's answer: a package named
`fiber` collides with `github.com/gofiber/fiber/v3` in the file importing both,
which is why OpenTelemetry's contrib packages are `otelgin`, `otelhttp`,
`otelfiber`. A prefix is necessary.

A draft of this section proposed unifying every satellite on the *project*
prefix — `ordofiber`, `ordogin`, `ordopgx` — on the grounds that the ecosystem
convention is `<project><library>`. **That reading of the convention is wrong, and
the owner caught it.** In `otelgin`, `otel` is not the *project* prefix; it is the
*subsystem*. OpenTelemetry happens to be single-purpose, so the two coincide. A
framework with several subsystems has to generalise the subsystem, not the
project — otherwise the first time a second subsystem needs a Fiber integration
there is nowhere to put it. `ordofiber` would claim the whole project×`fiber` cell for
whichever subsystem got there first.

**The rule is `<subsystem><library>`**, which is what the existing code already
does and what makes the grid work:

| | fiber | gin | net/http | pgx | database/sql | grpc |
|---|---|---|---|---|---|---|
| **crud** | `crudfiber` ✓ | `crudgin` ✓ | `crudnet` ✓ | `crudpgx` ✓ | `crudsql` ✓ | `crudgrpc` → |
| **i18n** | `i18nfiber` | `i18ngin` | — | — | — | — |
| **auth** | — | — | — | — | — | — |

So the satellites this document adds are named for the subsystem they extend, not
for the project: an observability module instrumenting the `crud.Source` and
`port.Service` seams is `obsotel`, a JWT adapter feeding `security.Policy` is
`authjwt`, a queue transport over the port layer is `portkafka`. Naming it
`portkafka` also settles an argument by construction — the review disputed that a
queue is a "port transport family," and a name that has to claim it is a claim
worth testing before the module exists.

**Root-module packages take no prefix at all** — `crud`, `query`, `errs`, `port`
— because nothing collides. A prefix appears only where an identifier would clash:
`ordoflag` cannot be `package flag` because the standard library owns that name, and `ordo` is short enough to be its own prefix so no abbreviation is invented.
That is the same rule as `crudfiber`, applied to a different collision, and it is
the whole of the naming convention:

> **A package is named for what it is. A prefix is added only to break a
> collision, and the prefix names the subsystem.**

---

## 7. The enforcement is broken today, and that is the finding that matters most

Rule 2 of the old draft said `make tidy` and D-033's `go list -deps` check were
the enforcement. Neither works. All three were measured on this tree:

- **D-033's own proof command prints 17 lines on a clean tree.** It ends in
  `grep '\.'`, which matches standard-library paths like
  `crypto/internal/entropy/v1.0.0`. It has never had pass/fail semantics. The
  correct form prints nothing:
  ```
  go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... | grep -v '^github.com/shardit-io/qq'
  ```
- **`go.work` hides a root-module dependency leak completely.** A package in the
  root module importing a satellite's dependency builds green, vets green, and
  leaves `go.mod` untouched — because the workspace build list is the union of
  every member. `make unit` and `make vet` both run with the workspace active, so
  **the repository has never once verified the resolution a consumer gets.** The
  consumer sees `no required module provides package …`.
- **`make tidy` cannot run**, and `test/go.mod` is untidy right now: with
  `GOWORK=off` it fails on a missing `go.sum` entry, and it requires
  `http/crudgin` while naming none of gin's transitive set. [[D-033]] frames this
  as a first-tag problem that disappears; it does not. Every release that adds a
  root-module package — and this document adds eight — reopens the window, because
  a submodule resolves the library from the *previous* tag.

So the enforcement rules are these, and they are Makefile work, not prose:

1. The `.Standard` check above, per module, in CI.
2. `GOWORK=off` builds and `go mod tidy -diff` per module, so what a consumer
   resolves is what gets tagged. Tidy submodules under a **transient** `replace`
   that is dropped afterwards, so no `replace` survives in a published `go.mod`.
3. `MODULES` derived with `find . -name go.mod`, not hand-listed — today a new
   submodule silently escapes `unit`, `vet`, `tidy` **and** `release`. Same for
   `make fmt`, whose hand-written directory list contains none of the packages
   this document adds, and for `go.work`, which `go work use -r .` generates.
4. `git push --atomic` with every tag in one push, and idempotent tag creation.
   Twelve sequential pushes fail partway eventually, and the proxy and sumdb do
   not forget.
5. **A dependency-diff gate, which is what grpc-go used instead of a module
   boundary.** `grpc-go#7766` snapshots `go list -deps` for **every public
   package** and fails the pull request on any change, so dependency creep needs
   an explicit reviewed diff. Kubernetes does the same thing with
   `import-restrictions.yaml` and `go mod graph | grep`. Both projects enforce the
   boundary with a *check* rather than a *module*, and a check is the only thing
   that works inside a workspace.
6. `retract` in the root `go.mod` as part of the release vocabulary. MVS has no
   upper bound, so lockstep **cannot** be mechanically enforced: `go get rx@v0.2.0`
   followed by `go get rx/http/crudgin@v0.1.0` builds fine, and the dangerous
   direction — old binding, new library — is the one a bare `go get -u` produces.
   [[D-033]] should say "process, and here is why mechanism is impossible."

**And one measurement that qualifies [[D-033]]'s premise honestly.** A minimal
gRPC client importing the notoriously heavy `google.golang.org/grpc` — one module
carrying xds, ALTS, OpenTelemetry, GCP auth, spiffe and gonum — resolves **5
modules, 47 external packages, a 15.7 MB binary**. Adding one blank import of
`grpc/xds` takes it to 30 modules and 42.3 MB, *from the same module*. Module
granularity is not what controls what a consumer pays; the **import graph** is,
via pruning, `go mod tidy` and dead-code elimination.

That does not make D-033 wrong — a satellite still keeps gin's 58-module graph out
of a `database/sql` consumer's version selection, which is real and measured. It
does mean the benefit is narrower than "modules keep consumers slim," and that a
subsystem should be split for **release cadence** first and dependency weight
second. Which is exactly the ordering `errs` was decided on.

---

## 8. Lockstep does not survive the scope, and OpenTelemetry already proved it

[[D-033]] rule 4 requires one version across every module. That is right at four.
OpenTelemetry-Go is the closest analogue in the ecosystem — 28 modules in the core
repository, 68 in contrib — and it **abandoned global lockstep**: `versions.yaml`
carries four independent version lines in core and eight in contrib, because one
number cannot express "the API is stable and the log SDK is not." That is the same
JPA-and-Hibernate split this document is built on, applied to release policy.

They needed a tool for it — `multimod`, whose `verify` enforces three invariants no
Go command checks: every module belongs to exactly one set, no two sets share a
non-zero major version, and **a stable module never requires an unstable one**.
A full release is roughly 85 tags across two repositories and takes a median of
one to two days.

Kubernetes pays the same bill in a different currency. Its publishing pipeline
has a permanently-open GitHub issue whose entire purpose is to be a failure
alarm: the bot reopens it with a log tail whenever a run fails, and it has
accumulated **over 1,600 comments since 2017** — one sample window shows 13
failures in 34 days. Their own summary of how they got there, from a maintainer
in the thread arguing to leave: *"We got into the staging mess… TL;DR; this is not
a technical problem."*

Three things follow for `ordo`, and the third is the uncomfortable one:

- **Two module sets, not one version.** `stable` and `experimental`, so a
  designed-yesterday package cannot ship under the same number as a `crud` tested
  across four engines.
- **Stay `v0.x` until the scope settles**, and the reason is sharper than
  convenience. Russ Cox gave Kubernetes three options in 2018: promise API
  compatibility and use `v1.X.Y`; allow breaking changes per release and carry
  `/vN` in the import path forever; or make no promises and use `v0.X.Y`. They
  took the third, deliberately, and `client-go` has been `v0.x` across 645
  versions since. Kratos took the second and its v2→v3 bump renamed **every**
  contrib module path. Under lockstep a major version costs N directory renames
  and every consumer's import block, so `v0.x` is what buys the freedom this
  document's own §12 list needs.
- **The documentation goes stale faster than anyone maintains it**, and that
  cost lands hardest on a repository whose best asset is its docs. Kubernetes —
  with a paid release team — currently has **four** documents that misdescribe
  its own module layout: the community staging guide still says the staged repos
  are "symlinked into Kubernetes' `vendor/` directory" (removed in 1.30, replaced
  by `go work vendor`), it lists published branches two releases out of date,
  `client-go`'s README says publishing happens "every day" when the interval is
  four hours, and `staging/README.md` lists 32 repos where `go.work` has 33. The
  structure generates facts that live in more than one place, and
  `CLAUDE.md` calls that a defect rather than untidiness.
- **The bill is people, not tooling.** OpenTelemetry's contrib CONTRIBUTING.md
  says it outright: *"The size of the OpenTelemetry Go developer community is not
  large enough to support an ever growing amount of components… Maintaining
  components here hampers the development of OpenTelemetry for Go and therefore
  should be avoided."* Between 61% and 76% of their pull requests are dependency
  bots. This repository today has 13 packages, 34 decision documents and one owner.

---

## 9. One dependency decision, not one requirement

[[D-033]] says "one external requirement each," and the first satellite this
document schedules already breaks it: `rpc/crudgrpc` needs `grpc`, `genproto` and
`protobuf`. The rule was never about counting requires — it is **one dependency
*decision***, one upstream project a consumer either wants or does not. Six
OpenTelemetry modules are one decision; grpc plus genproto plus protobuf is one
decision. That is checkable by prefix, which a count is not.

The same amendment settles a shape the draft did not notice: observability is
cross-cutting, so an OpenTelemetry middleware for Gin needs gin *and* otel — two
decisions — and therefore has no legal home in either module. The answer is that
such a module instruments the **seams** (`crud.Source`, `port.Service`) and never a
binding, which is the right layering anyway.

---

## 10. What this does not become

Not a dependency-injection container, and not an annotation processor. The
framework's Spring inheritance is its module structure and its
contract-versus-implementation discipline — not its runtime magic. And the record
in Go is unusually clean about which way this goes: gorm, pgx, fiber, ent and sqlc
widened their surface **within one domain** and thrived; Beego, Revel, Buffalo and
go-kit widened the **domain** and faded. The `?` list above is close to Beego's
inventory item for item, which is why it is a list with a bar in §12 rather than a
diagram.

---

## 11. The renames — one done, one decided

There are two, and conflating them is how a reader ends up editing the wrong
import path.

**Rename 1: `go-rx-crud` → `rx`. Done.** Across every module, every document and
every import. It settled three things that were open while this document was
being written:

- **`crudport` became `port`.** Under `go-rx-crud` the `crud` prefix carried its
  weight; the transport-neutral layer is not CRUD-specific and should not claim
  to be.
- **`cmd/rxcrud` became `cmd/rx`**, one CLI with subcommands — and becomes
  `cmd/ordo` under rename 2.
- **The `errs` import path stopped being an argument.** the errors roadmap §4 had conceded that a
  service wanting only the error contract had to import a CRUD library. That
  objection died here, and the errors roadmap §4's remaining question — whether `errs` is its own
  module — was then answered yes on release-cadence grounds.

**Rename 2: `rx` → `ordo`. Decided, not yet executed.** `github.com/shardit-io/qq`.

The reason is discoverability, and it is not a matter of taste. **`rx` in Go means
ReactiveX.** `ReactiveX/RxGo` has roughly 5,100 stars and its package is literally
named `rx`; `reactivego/rx` and `si3nloong/rx` exist too. Every search for "go rx"
lands on reactive streams, which is a permanent tax on a framework whose adoption
depends on being found at all.

`ordo` is Latin for order, rank, and an official register — the ordering of
queries and the registry of models, which is both halves of what this library
does, and `ORDER BY` makes it land for anyone writing SQL. Verified rather than
assumed: `proxy.golang.org` returns 404 for the module path, no Go package uses
the identifier with any adoption, and it collides with nothing in the standard
library. It also satisfies the constraint the owner set — **at four letters it is
its own prefix**, so `ordoflag` and `ordocfg` need no invented abbreviation, which
`stele`→`ste` or `pinax`→`pnx` would have.

The organisation stays `shardit-io` for now. It is a weak name — "shard it"
promises horizontal sharding this library does not do, and does not own the
caller's connection either — but renaming the organisation and the module at once
doubles the breakage. §12 keeps it.

### What makes both renames cheap, and what ends it

**No tag has been pushed.** [[D-033]]'s "rough edge" note and `make tidy`'s error
message both turn on that, so each rename costs a `sed` and nothing else. Had a
single version been released first, every consumer would have needed a v1
forwarding module of type aliases, and the old path would have had to keep
publishing.

**The first tag is the point of no return for every naming question in §6.**
After `v0.1.0`, `rx → ordo` costs a deprecation cycle instead of a search and
replace, and a module boundary — `errs` moving out — costs more than a name.
That is the reason §1 is first in this document and why §12's remaining naming
question has a deadline rather than a priority.

### Three names are in play, not two

`github.com/shardit-io/go-rx` exists on GitHub as an older version and is the
module path `../old-rx` still declares; `go-rx-crud` is vacated; `rx` is current;
`ordo` is the target. The proxy and the checksum database remember `go-rx` at
whatever it was tagged, so that path is not free to reuse for something with a
different meaning. When `rxflag` and `rxcfg` move across as `ordoflag` and
`ordocfg` (§4), they should arrive as a deliberate copy with the old path left
alone, not a redirect.

### The lesson from doing it once

Rename 1 took two commits, and the gap between them is the useful part.
`b70cd71` changed every import and left `make release`'s lockstep check grepping
each submodule's `go.mod` for the **old** module path — a release would have
failed on every submodule with a message about the wrong version, which is not
what the message says. `7ac4724` caught it. A `sed` across a repository is one
edit; the things that break are the strings that are **not** imports, and a check
that greps for a module path is exactly that shape. Rename 2 should start by
listing those: `Makefile` (`version`, `release`), `go.work`, every `go.mod`,
`//go:generate` lines, `docs/`, `README.md` and `_examples/`.

---

## 12. Not decided yet

Left open on purpose. Both expire at the first tag (§11).

- **The bar for everything on §3's `?` list.** `log`, `i18n`, `health`, `migrate`,
  `app`, `portkafka`, `obsotel`, `authjwt`. The bar is the one §2 states: a package joins
  the contract manifest when a **second** implementation needs it, and never when
  the standard library already contracts the thing. `log` fails on the second
  clause outright — `slog.Handler` is the seam. `i18n` is `errs.MessageSource`
  until a second subsystem wants it, and then it is a move, not a design.
  Configuration is **settled and off this list**: `ordoflag` and `ordocfg` are
  implementations, adopted from `old-rx`, with no contract at all (§4).
- **Whether the organisation is renamed too.** `shardit-io` stays for now (§11).
  "Shard it" promises horizontal sharding this library does not do, and
  `github.com/shardit` is held by another account, so the `-io` suffix is a
  workaround rather than a choice. If it is ever fixed, the cheap moment is the
  same one §11 names — before the first tag, and ideally in the same change as
  `rx → ordo` rather than as a third rename.

  The three naming questions that used to sit here are settled: the satellite
  prefix is `<subsystem><library>` (§6), `errs` gets its own module with [[D-033]]
  amended to *no third-party requirement* (the errors roadmap §4), and `rx` becomes
  `ordo` (§11).
