# D-137 — A wired source carries the chain the deployment declared

**Status:** accepted
**Extends:** [[D-111]] (contribution as data, and silence as a refusal), [[D-074]]
**Constrains:** [[FL-021]], [[UC-030]]
**Invariant:** `crudsqlfx` hands out the composed source. The chain around it is
`crudsqlfx.Layers`, written by the deployment, outside in; a `crudsqlfx.Wrapping`
carries a name and a function and no place of its own. A declared layer nobody
contributed, a contributed layer nobody declared, one name twice, a nameless or
functionless layer and a layer that answers with no source are refusals that
fail the graph, and a refused chain is put around nothing at all. The source the
module wires is separately addressable as the base, so a graph that cannot reach
a database supplies one and keeps the chain.

## The decision

An application that wants telemetry, a slow-query log or a replica router around
`crud.Source` has one source: the one this module wired, because building it
means reading the schema and the module is what bounds that read. Until now the
only way to put a layer on it was `fx.Decorate` in the composition root, and
that is not a seam — it is a collision.

**fx has one mechanism for replacing a value and for decorating it, and refuses
two of them in one scope.** `fx.Replace` *is* a decorator. A composition root
that decorates `crud.Source` therefore breaks every test harness that replaces
it with a source that does not reach a database — the graph fails to build with
`already decorated`, and the harness that proves the deployment starts is the
first thing to go. Moving the decorator into a module beside the consumers
builds green and covers nothing: a decorator reaches the scope it was declared
in and that scope's descendants, so a layer declared in a neighbouring module
instruments nobody and says nothing about it. Both failures are properties of
where a decorator sits, which is exactly the kind of fact a composition root
should not have to know.

**So the seam belongs to the module that builds the source.** One provider, one
composed value, and every scope that resolves `crud.Source` — a constructor, an
invoke, a root `fx.Populate` — gets the same wrapped source. There is no
placement to get right and nothing that changes meaning when a consumer moves
between modules. `storage.Middleware` and `port.ServiceMiddleware` are the same
answer for their subsystems; this is the fx form of it for the one source a
graph resolves.

**The chain is declared by the root, not carried by the contributions.** An
`Order` field on each contribution was the first shape and it is the wrong one:
a contributor cannot know what the other layers are, so the numbers become a
coordination scheme by magic constant — 10, 20, 30 and a collision nobody
predicted. The composition root is the one thing that knows both layers exist
and which of them belongs outside, so it writes the chain as names in order and
the contributions carry none. `app.Ordered` with `app.Sorted` is not the shape
here either, for the same reason: it exists to sort contributions that carry
their own place, and it breaks a tie on `Order` by name, which is right for a
middleware chain where every order is a valid chain and wrong for a source,
where a replica router inside a timer measures something else than one outside.

**A layer that did not arrive is a failed start, not a quiet absence.** The
declaration is what makes that possible, and it is why both directions refuse.
A contribution that forgets `AsWrapping` is provided, consumed by nobody and
otherwise invisible — the graph builds and measures nothing. A module that
contributes a layer this deployment never wrote down is the same failure from
the other side: the root did not ask for it and cannot say where it goes. So a
declared name that nobody contributed and a contributed name nobody declared
both stop the start, and both name what is wrong. This is [[D-111]]'s rule
applied to layers: nothing counts that was not written down, and silence is the
refusal rather than a smaller chain.

**A refusal is complete.** The whole declaration and the whole group are checked
before the first layer is applied, so a graph never starts with half a chain
around its source. A layer that answers with `nil` is a refusal rather than a
silently skipped layer: telemetry that quietly is not there is worse than a
start that stops.

**A contributed layer answers for what it wraps.** [[D-061]] is the obligation
and it is the contributor's, not this module's: a layer that hides `Begin` takes
transactions with it, and a bare type assertion downstream will not find what
the layer forgot to forward. This module composes; it does not repair.

**The base is addressable so that a harness keeps the chain.** A graph that
cannot reach a database used to replace `crud.Source`, which replaces the
composed value: the layers the deployment declared were skipped, and the fast
harnesses that prove a deployment builds stopped covering its chain entirely.
The source this module wires is therefore provided under `name:"vv.crudsql.base"`
and `crudsqlfx.Base` is the gesture for supplying one instead. Replacing
`crud.Source` itself remains possible and still means the other thing — this
value is the source, layers included — but it is now a choice beside an
alternative rather than the only way to run a graph offline.

## What it forbids

- Do not give a contribution a place of its own — an `Order`, a priority, a
  position argument. The chain is `Layers`, and it is one list in one place.
- Do not infer a layer from a provider, a configuration flag or the presence of
  an optional module, and do not let a missing or unexpected layer degrade into
  a shorter chain.
- Do not apply a partial chain, and do not let a refusal answer with an
  unwrapped source.
- Do not add a second way to put a layer on this source — a module option
  carrying functions, a variadic argument to `Module`, an implicit decorator.
  Two spellings for one composition is how the chain stops being knowable.
- Do not give `crud` an fx-shaped middleware type for this. The contribution is
  data owned by the binding; the root package stays free of the container
  ([[D-037]], [[D-074]]).

## Where it lives

| File | What it holds |
|---|---|
| `crud/adapter/crudsql/crudsqlfx/wrapping.go` | `Wrapping`, `AsWrapping`, `Layers`, `Base`, the group, the base name, the sentinels, the composition |
| `crud/adapter/crudsql/crudsqlfx/crudsqlfx.go` | `Module`, the base provider that wires the source, the provider that hands out the composed one |

## Proven by

| Test | What it pins |
|---|---|
| `crudsqlfx/wrapping_test.go:TestTheChainIsTheOneTheDeploymentWroteDown` | the declaration is the order, outside in |
| `crudsqlfx/wrapping_test.go:TestTheChainDoesNotDependOnTheOrderTheGroupArrivedIn` | one group in two arrival orders builds one source |
| `crudsqlfx/wrapping_test.go:TestASourceNobodyDeclaredALayerAroundIsTheSourceItself` | an empty declaration is the source itself |
| `crudsqlfx/wrapping_test.go:TestARefusedChainIsPutAroundNothingAtAll` | a refusal leaves no half-wrapped source |
| `crudsqlfx/wrapping_test.go:TestWhatIsMissingAndWhatWasNeverDeclaredAreBothNamed` | both refusals name every layer, deterministically |
| `crudsqlfx/wrapping_test.go:TestTheRefusals` | the seven refusals, each by sentinel |
| `crudsqlfx/wrapping_test.go:TestTheSourceTheGraphHandsOutCarriesTheDeclaredChain` | the graph hands out the composed source |
| `crudsqlfx/wrapping_test.go:TestASourceNoDeploymentWrappedIsTheSourceTheModuleWired` | the control: no declaration wraps nothing |
| `crudsqlfx/wrapping_test.go:TestAContributionThatNeverReachedTheGroupStopsTheStart` | a forgotten `AsWrapping` is a failed start, not a silent absence |
| `crudsqlfx/wrapping_test.go:TestALayerNoDeploymentDeclaredStopsTheStart` | a layer nobody wrote down never reaches the source |
| `crudsqlfx/wrapping_test.go:TestAReplacedBaseStillCarriesTheDeclaredChain` | an offline harness keeps the declared chain |
