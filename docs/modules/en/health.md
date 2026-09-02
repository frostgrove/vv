# health — readiness a platform can route on

```go
import "github.com/frostgrove/vv/health"            // the registry and its projections
import "github.com/frostgrove/vv/health/healthfx"   // uber/fx: the check group
```

**Module:** `health` is in the root module — standard library only.
`healthfx` is a module of its own because it takes uber/fx ([[D-033]])
· **Depends on:** nothing · **Depended on by:** nothing in the framework, on
purpose ([[D-091]])

Three answers to three different readers: an orchestrator asking whether to
restart this process, a load balancer asking whether to send it the next request,
and an operator asking which dependency is the reason.

---

## What you get

### `health` — the registry

| | |
|---|---|
| `Contribution` | one dependency: `Name`, `Code`, `Importance`, `Timeout`, `Probe` |
| `Probe` / `ProbeFunc` | `Check(ctx) error` — and the adapter for anything that already looks like it |
| `Importance` | `Required` · `Degrading` · `Informational` · `Disabled` |
| `Spec` | `Contributions`, `Timeout`, `Freshness`, `Now` |
| `New(spec)` | the explicit constructor; refuses every registration problem at once |
| `Auto(contributions…)` | the same thing with defaults |
| `Registry.Live()` | `Report{Status: StatusLive}`, running nothing |
| `Registry.Ready(ctx)` | the public projection: status and stable codes |
| `Registry.Inspect(ctx)` | the operator projection: names, messages, durations |
| `Registry.Contributions()` | what was accepted, after defaults |
| `Report` / `Detail` / `CheckDetail` | the projections, JSON-tagged |
| `Status` | `StatusLive` · `StatusReady` · `StatusDegraded` · `StatusDown` |
| `State` | `StatePassing` · `StateFailing` · `StateDisabled` |
| `ErrRegistration` / `RegistrationError` | the refusal, with `Problems()` |
| `MaxMessageBytes` · `DefaultTimeout` · `DefaultFreshness` | `256` · `2s` · `1s` |

```go
registry, err := health.New(health.Spec{
    Freshness: 2 * time.Second,
    Contributions: []health.Contribution{
        {Name: "postgres.primary", Code: "database", Importance: health.Required,
            Probe: health.ProbeFunc(pool.Ping)},
        {Name: "redis.sessions", Code: "cache", Importance: health.Degrading,
            Probe: health.ProbeFunc(redis.Ping)},
        {Name: "clamav", Importance: health.Disabled},
    },
})
```

### `healthfx` — the check group

| | |
|---|---|
| `AsCheck(ctor)` | annotates a constructor so its `health.Contribution` joins the group |
| `Registered` | the group, as an `fx.In` parameter object |
| `Spec` | `Timeout`, `Freshness` |
| `Checking(spec)` / `Auto()` | provides the `*health.Registry` over whatever joined |

```go
fx.Options(
    fx.Provide(healthfx.AsCheck(func(pool *pgxpool.Pool) health.Contribution {
        return health.Contribution{
            Name: "postgres.primary", Code: "database",
            Importance: health.Required, Probe: health.ProbeFunc(pool.Ping),
        }
    })),
    healthfx.Auto(),
)
```

A module contributes one check and knows nothing about the registry, the
transport or the other modules' checks. Two modules claiming one name is a
start-up failure.

### The Fiber routes

`appfiber.Health` mounts the three projections as an ordinary contributed route,
so the boot access gate sees them like any other. See [app](app.md).

## Importance is yours, not the checker's

The same Redis ping is required in a session service and degrading in a report
exporter. So a `Contribution` carries the probe *and* the decisions the program
makes about it, and both are set where the program is assembled.

That is also why there is no `jobspghealth` package and never will be. `Probe` is
one method; the adapter from a driver to a contribution is two lines in your
composition root, and ten checked dependencies are ten of those rather than ten
packages. [[D-091]].

## Liveness runs nothing

`Live()` reaches no dependency. A liveness probe that pings the database restarts
every replica of every service the moment that database is slow — cold caches and
a reconnect storm as the recovery plan, at the worst possible time. [[D-090]].

## Degraded keeps serving

`StatusDown` is the only status the Fiber binding answers 503 to. A replica
missing a degrading dependency still serves everything that does not need it, and
because replicas share their dependencies, removing "the degraded one" removes
all of them. It says `degraded` in the body instead. [[D-090]].

## Two projections, and the poor one is deliberate

`Ready` returns a status and the stable codes of the checks that produced it. A
contribution with no `Code` moves the status and names nothing: publishing a word
to unauthenticated callers is a decision, and the default is not to.

`Inspect` returns everything — names, importances, bounded messages, durations —
and is meant to sit behind a permission, which is what `appfiber.Health` does
with its `Operator` field.

## One pass, shared

A readiness endpoint is scraped by every probe, dashboard and load balancer a
deployment has. `Freshness` reuses a completed pass; concurrent callers join the
pass in flight rather than starting their own; and the pass does not inherit a
caller's cancellation, so one scraper giving up cannot fail the answer everyone
else is waiting for ([[D-084]]). Each check still runs under its own timeout, and
a panicking probe fails its own check and nothing else.

## See also

- [app](app.md) — `appfiber.Health`, and the gate that checks the declarations
- [runtime](runtime.md) — `Supervisor.Ready`, the seam a background worker
  answers through
- [[D-084]] · [[D-090]] · [[D-091]] · [[FL-027]] · [[UC-025]]
