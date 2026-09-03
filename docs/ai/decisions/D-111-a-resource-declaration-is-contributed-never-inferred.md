# D-111 — A resource declaration is contributed, never inferred

**Status:** accepted
**Invariant:** The eviction-domain rule of [[D-104]] runs in every deployment
that wires caches, because `cachefx` activates with declared resources
required and a start whose cache shares a resource with durable state fails
with `*cache.ActivationError`. A declaration is a `cache.ResourceDeclaration`
value carried by an fx group, so the package that lives on the resource never
imports `cache` or `cachefx` to be counted, and nothing derives a declaration
from a provider: an undeclared resource stays undeclared until somebody writes
it down.

## The decision

[[D-104]] made the rule checkable and `cache.Activate` checks it. Nothing called
`cache.Activate`. There was no fx binding for the cache subsystem, so the only
callers were the cache's own tests: the invariant that a cache never shares a
Redis with the revocation list held in the test suite and nowhere a deployment
could reach it.

`cachefx` is the caller. It is the same shape as `healthfx` and `jobsfx` — an
fx group per kind of contribution, a magic form for the ordinary deployment and
a low-level form underneath — and it makes two choices the other bindings do
not have to make.

**The strictness is inverted relative to the core.** `RequireDeclaredResources`
is off by default in `cache` because the rule has to be adoptable one resource
at a time, and a default that refused at boot would be adopted by deleting the
check. A graph that reaches for `cachefx` has finished adopting it: it is the
composition root, it knows every provider it built, and it is the one place that
can answer. So `cachefx.Caching` requires declarations unless the spec writes
`Undeclared: cachefx.Accepted`, and silence is the refusal. The permissive
answer stays reachable, as a word rather than as a forgotten zero value.

**Nothing is inferred from a provider.** Every provider already names a resource
identity, so a binding could quietly declare `CacheTenant` for each one and
satisfy `RequireDeclaredResources` without a single line written by a person.
That would invert the rule: the declaration is what the check is made of, so
deriving it from the thing being checked turns every undeclared resource into a
proven-separate one, and the root that forgot to describe its Redis would get
silence back instead of a refusal. It would also collide with the truth when the
root does describe that Redis, because one resource is declared once.

**The contribution is data, so the durable subsystems stay independent.**
`revokeredis` holds the revocation list and `jobsredis` holds queued work, and
they are the two tenants the rule exists to protect. Neither may import `cache`
to say so: a base package that imports a subsystem to be described by it is the
first step toward a package named after both of them, and the framework's
packages are joined by extension points rather than by imports. So the
extension point is an fx group of plain values. The composition root supplies
the declaration for a package that cannot speak for itself
(`cachefx.Resources`), and a package that can speak for itself contributes
through `cachefx.AsResource` or through the group tag `group:"vv.cache.resources"`
written out, which needs no import of `cachefx` either. `cachefx` exports no
constructor for a declaration for the same reason: a convenience spelling would
pull contributors into importing the binding to write a value the core already
defines.

**Activation belongs to the start, not to the graph.** `cachefx.Activating`
appends one lifecycle hook. Publishing the caches while the graph is being built
would be earlier, and a refusal there rolls nothing back — every pool and client
already constructed is leaked, because their stop hooks belong to a start that
never happened. On the start hook the refusal is a start failure fx unwinds.

## What it forbids

- Do not make `revokeredis`, `jobsredis`, `jobspg` or any other durable package
  import `cache` or `cachefx` in order to declare where it lives. The
  declaration is data; the root or a neutral group contribution carries it.
- Do not derive a `ResourceDeclaration` from a `cache.Provider`, from a backend
  description, or from anything else the check is made against.
- Do not make the magic form permissive. `cachefx.Auto` and `cachefx.Caching`
  require declared resources; `cachefx.Accepted` is how a deployment still
  adopting the rule says so, and `cachefx.Activating` is how a root that builds
  its own `cache.ActivationSpec` says anything at all.
- Do not publish caches from an `fx.Invoke` or a constructor. The activation is
  a start hook so that a refusal unwinds the start.
- Do not add a declaration constructor to `cachefx`. `cache.ResourceDeclaration`
  is the vocabulary, and it lives where a contributor can reach it without the
  binding.

## Where it lives

- `cache/cachefx/cachefx.go` — the three groups, `Contributions`, the magic
  `Caching` and `Auto`, the low-level `Activating`, the `Resources` supplier and
  the `Undeclared` word that decides the strictness.
- `cache/cachefx/cachefx.go:activate` — the one lifecycle hook the binding
  appends, and the only place `cache.Activate` is called from.
- `cache/resource.go` and `cache/activation.go` — the rule itself, unchanged by
  this decision.

## Proven by

- `cache/cachefx/cachefx_test.go` — a cache set and a provider contributed by
  two modules that name nothing of each other reach one activation; a resource
  declared as holding revoked sessions or queued work fails the start with the
  eviction domain named and the cache unpublished, with or without a waiver; a
  resource nobody described fails the start until the deployment writes
  `cachefx.Accepted`; a declaration contributed through the raw group tag, with
  no call into `cachefx` at all, refuses the same start; the low-level form
  activates the spec it was handed and asks for nothing around it; no cache is
  published while the graph is being built and every one of them is published
  when the start finishes.
- `TestAPackageDeclaresItsResourceThroughTheGroupTagWithoutImportingThisBinding`
  is the one that pins the extension point: it contributes the declaration the
  way a package outside this repository would.
- `TestARefusedActivationUnwindsWhatTheStartHadAlreadyBroughtUp` is the other
  half of the start hook. A provider opens a pool on its own start hook, the
  cache it serves shares a resource with queued work, and the assertion is that
  the pool was opened and then closed again: the refusal unwinds the start
  rather than leaking what the start had already brought up. Publishing from a
  constructor or an `fx.Invoke` fails it at the graph, where nothing that opened
  is ever asked to close.
- `TestAGraphThatCannotSayWhatItIsActivatingIsRefusedBeforeItRuns` refuses a
  spec missing its application, its environment, a strictness nobody defined, or
  a cache set — and refuses it at `fx.New`, before a single hook runs. Each
  subcase contributes the provider and the declared resource that would have let
  the start succeed, so the spec is the only thing left that can refuse the
  graph, and the refusal names the field it came from.

## See also

[[D-033]] [[D-091]] [[D-096]] [[D-104]] [[D-108]] [[FL-025]] [[UC-024]]
