# D-110 — A module's contributions are inferred from what its packages construct and confirmed once

**Status:** accepted, applies [[D-109]]'s shape to the composition root
**Invariant:** the constructors a bounded context contributes to a container are
derived by `vv generate module` from what its package tree actually builds,
recorded in `module.manifest.yml`, and nothing compiles until a person has said
of each one which kind it is — or that it is not a contribution at all.

## The decision

[[D-106]] gave a module a value: `module.Definition` names constructors, sorts
them into five kinds, and a `Profile` decides which of them a process runs. What
it did not answer is where the list comes from. Today it comes from a hand-written
`fx.Module(…)` sheet in the consumer's composition root — two hundred lines of
identifiers, appended to by everyone, read by nobody, and wrong in exactly the
way nothing can see: a constructor written and never wired is a feature that
silently does not exist.

`vv generate module` reads the package tree instead.

- **The candidate** — a top-level function with no receiver, no type parameters,
  and a first result that is a *named* type. A function returning `string`,
  `[]Language`, `any` or only an `error` is a helper, not something a container
  builds, and is never offered. Under a subpackage the function must be exported,
  because the generated file could not name it otherwise; in the module's own
  package an unexported one is reachable and is offered.
- **The kind** — read off that result type against four marker types passed as
  strings: `health.Contribution` is a check, `runtime.Runner` is a worker,
  `app.Seeder` is a seeder, `appfiber.Route` is a route. Anything else is
  `provide`.
- **The manifest** — one row per candidate, with `kind`, `source:
  inferred-from-signature`, a fingerprint over the symbol, the inferred kind and
  the signature, and `confirmed: false`.

Writing `confirmed: true` accepts the row. Writing a different `kind` overrides
the inference and the row's `source` becomes `declared-in-manifest`. Writing
`excluded: true` says the symbol is not a contribution, and an excluded row is
never waited for: there is nothing to confirm about something that is not in the
module.

Confirmed, the generator emits one value:

```go
var VVModule = vvmodule.MustDefine(vvmodule.Spec{Name: "workspace", …})
```

which `appfx.Option(VVModule, profile)` turns into the fx graph.

**Inference is a draft, and the manifest is the record.** The signature of a
constructor is weak evidence of what it is *for* — a function returning a
`*Sweeper` may be a worker, a provider or neither, and no amount of parsing
settles it. So the generator does not act on an inference nobody has signed. This
is [[D-109]]'s argument, and the shape is deliberately the same one.

**A new constructor stops the build until somebody places it.** That is the
defect this exists for, and it reads in the direction the hand-written sheet
never could: the sheet was silent about what it omitted. A new exported
constructor appears as an unconfirmed row, and `vv_module_gen.go` becomes
`var VVModule vvModule = "confirm every contribution in module.manifest.yml"` —
`cachegen`'s shape verbatim ([[FL-025]]), for the same reason [[D-109]] takes it
rather than [[FL-029]]'s: a module that quietly lost a constructor is a feature
that is not wired, and finding that at boot is worse than finding it at build.

**Excluding is a first-class answer.** A confirmation gate that can only say yes
turns into a ritual. `excluded: true` is how a person says no once, and it
survives a signature change: an exclusion is a statement about the symbol, not
about its arguments. A module whose every row is excluded is refused at
generation rather than becoming a `Definition` that `Define` would refuse at
boot.

**The marker types are flags, not imports.** `internal/codegen` names
`health.Contribution` and `appfiber.Route` as default strings and links to
neither; the generated file imports only `app/module` and the module's own
subpackages. A consumer whose health contribution is their own type passes
`-check-type`, and `-route-type -` turns an inference off. A generator that
hard-wired `appfiber` would be the `otel_tenant_eventsource_` shape wearing a
tool's hat, and `appfiber` is a satellite module the root cannot import anyway.

**`-recursive` means one level, not the whole tree.** For the other three
generators the unit is a package. Here it is a package *tree*, so a walk that
recursed would make every subpackage a module of its own. Every directory
directly under `-dir` that holds Go files is one module — which is what
`src/mod/*` already is — and the walk collects: one run names every contribution
waiting for a person and, under `-check`, every stale module, each prefixed by
its directory.

## Why

**Because the doctrine had three layers for jobs and cache and one for module.**
`jobs.Auto`, `cache.Auto` and `module.Auto` are the same promise, and the third
was a promise a consumer could not keep: `module.Auto("workspace", …)` still
needs the list, and writing the list by hand is the thing being replaced.

**Because `--check` is only worth having where a person cannot hold the
invariant.** A route that stopped agreeing with its guard, a wire body that grew
a field, a cache with no declared scope — and a module missing a constructor
somebody wrote last week. The first three had a generator and a manifest; this
one had a two-hundred-line sheet and a code review.

**Because the compiler is the only reliable reader.** The manifest lists symbols,
the generated file names them, and a symbol that is deleted or renamed fails to
compile in the generated file rather than lingering as a manifest row nobody
reads. A row whose symbol has disappeared is simply dropped on the next run.

## What was rejected

**Taking every exported function, without the named-result rule.** It sweeps in
`Label() string`, `AsCheck(any) any` and every other helper, and the first
confirmation pass becomes a hundred exclusions. The rule keeps the noise off the
person whose signature the gate needs.

**Requiring the `New` prefix.** It is a convention, not a fact, and half of a
real composition root's constructors are unexported closures over configuration
with names like `translationPolicy`. Filtering on a naming convention would have
been silent under-inclusion — the exact defect the generator exists to catch.

**Reading the existing `fx.Module` sheet and rewriting it.** Then the sheet stays
the source and [[D-073]]'s rule is inverted: what runs would be derived from a
document. The package tree is what runs.

**Type-checked loading with `go/packages`.** The other three generators read
source text with `parser.ParseDir` and this follows them, for the reason
[[D-109]] gives: what is needed is identifiers, and comparing identifiers is what
the manifest does.

## Proven by

- `TestTheKindOfAContributionIsInferredFromWhatItsConstructorReturns` —
  `internal/codegen/module_test.go`, with `Names`, `contract.Label` and
  `contract.newHidden` as the controls that a helper, a primitive result and an
  unexported subpackage function are not offered.
- `TestAnUnconfirmedModuleLeavesAFileThatWillNotCompile`.
- `TestAConfirmedModuleBecomesOneDefinitionSortedIntoTheKindItWasGiven`.
- `TestChangingWhatAConstructorReturnsWithdrawsOnlyItsOwnConfirmation`.
- `TestAKindWrittenIntoTheManifestOutranksTheOneInferredFromTheSignature`.
- `TestAContributionExcludedByHandIsNeitherWaitedForNorGenerated`.
- `TestAModuleWhoseEveryContributionIsExcludedIsRefusedRatherThanWritten`.
- `TestTheModuleCheckReportsDriftWithoutWriting`.
- `TestAPackageThatAlreadyDeclaresTheModuleVariableKeepsItsOwn`.
- `TestTheGeneratedDefinitionCompilesAndActivatesByRole` — a real module,
  generated, built and run: the worker is active under `module.Complete` and held
  back under `module.Base`.
- `TestTheRecursiveModuleRunNamesEveryContributionWaitingAndSaysWhichModuleItIsIn`,
  `TestTheRecursiveModuleCheckNamesEveryStaleModuleAndNotOnlyTheFirst` —
  `internal/codegen/check_test.go`.
- `TestTheModuleSubcommandRefusesAContributionNobodyConfirmed`,
  `TestTheModuleSubcommandHasItsOwnFlags` — `cmd/vv/main_test.go`.

## See also

[[D-109]] [[D-106]] [[D-096]] [[D-073]] · [[FL-032]] [[FL-030]] [[FL-031]]
[[FL-025]] · [[UC-014]]
