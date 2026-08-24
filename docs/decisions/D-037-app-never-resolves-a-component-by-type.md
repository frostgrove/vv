# D-037 — `app` never resolves a component by type

**Status:** accepted — narrows [[D-021]]
**Invariant:** no component is ever looked up by its type; every dependency is passed as a Go value at a call site the consumer wrote; `app` holds no `map[reflect.Type]any` and no equivalent registry.

## The decision

If an `app` package is ever written — the composition root: signal handling,
ordered shutdown, the forty lines every Go service copies — it may hold a list
of things to run and the order to run them in. It may not hold a way to *find*
one.

`app.Run(ctx, ...func(context.Context) error)` is the shape. A component graph
keyed by type is not.

## Why

**Because [[D-021]] is the licence for a dependency-injection container, not the
guard against one.** Its invariant is that the magic must fail at build or
start-up rather than at request time. A container built and validated inside
`Run()` satisfies that completely — it would fail loudly, at start-up, exactly
as D-021 asks. So D-021 cannot be the thing that stops it, and
`ROADMAP-framework.md` was wrong to imply it could by saying only "not a
dependency-injection container". That is an intention with no mechanism.

**Because the slide has three steps and each looks reasonable.**

1. `app.New().Add(server).Add(worker).Run(ctx)` — a component list. Nobody
   objects.
2. The worker must start after migrations, so `Add` gains ordering or
   `DependsOn`. Now there is a graph.
3. The worker needs the repository the server also holds. Either `main` passes
   it — in which case the graph was pointless — or `app` resolves it. Resolution
   means lookup by type. That is a `map[reflect.Type]any`, and it is a container.

The refusal has to be written down at step 0 or it does not survive step 3.

**Because this is what §6 of the error roadmap already does for the analogous
risk.** It refuses an `init()`-time registry with a mechanism and three named
reasons rather than an intention. `app` gets the same treatment.

**What is left is worth having.** `signal.NotifyContext`, an `errgroup`, ordered
`Shutdown` with a deadline. Every Go service writes it and most write it
slightly wrong. That is a package. It is not a subsystem, and it needs no
contract — which is why `app` is not on the manifest in [[D-035]].

## What it forbids

- Do not key anything on `reflect.Type`. Not a map, not a slice searched by
  type, not a generic `Get[T]()`.
- Do not add a `DependsOn`, `Requires` or `Provides` to a component. Ordering is
  the order the consumer passed them in.
- Do not accept `...any` and type-switch it. That is the same lookup wearing a
  different shape.
- Do not cite [[D-021]] as permission for any of the above. D-021 permits magic
  that fails early; this decision is narrower than D-021 on purpose.
- Do not read this as forbidding reflection generally. `crud` is built on it.
  This is about *resolving a dependency*, not about inspecting a model.

## Where it lives

Nowhere yet — `app` is unwritten, and `ROADMAP-framework.md` §12 keeps it behind
a bar. This decision exists so that the first person to write it finds the rule
already there rather than discovering it after step 3.

## Proven by

Nothing, yet. When `app` lands, the check is a test that the package imports
`reflect` for nothing, or does not import it at all:

```
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./app
```

and a reading of the exported surface for a lookup by type. Until then this is a
rule with no code to break, which is the cheapest moment to write it.

## See also

[[D-021]] [[D-035]] [[D-001]] [[D-022]]
