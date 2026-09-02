# FL-027 — A dependency becomes a health answer

**Entry points:** `health.New` / `health.Auto`, `health.Registry.Live` /
`:Ready` / `:Inspect`, `appfiber.Health`
**Governed by:** [[D-084]] [[D-090]] [[D-091]]

What happens between a composition root registering one dependency and an
orchestrator, a load balancer or an operator getting an answer.

## Registration

1. **`health.Contribution`** — `health/health.go` — one dependency as the
   composition root sees it: `Name` (operator identity, unique), `Code` (the
   optional stable word a public body may carry), `Importance`, `Timeout`, and
   a `Probe`, which is one method and may be a `ProbeFunc` over any existing
   `func(context.Context) error`.
2. **`health.New`** — `health/registry.go` — applies the default per-check
   timeout and freshness window, then `accept` validates. Every independent
   problem is collected rather than the first returned: a missing name, a
   duplicate name, an unknown importance, a probe-less contribution that is not
   `Disabled`, a negative budget, two contributions publishing one code. The
   refusal is `*RegistrationError`, which satisfies `errors.Is(err,
   ErrRegistration)` and exposes `Problems()`.
3. **`health.Auto`** — the same constructor with defaults and no `Spec`, for the
   common case.
4. Accepted contributions are sorted by name, so two runs of one build produce
   the same operator page in the same order.

## Fx registration

`health/healthfx/healthfx.go` — `AsCheck` annotates a constructor into the
`vv.health.checks` group; `Registered` collects it; `Checking(spec)` (and
`Auto()` over it) provides the `*health.Registry`. A module contributes one
`health.Contribution` and knows nothing about the registry, the transport, or
the other modules' checks. Two modules claiming one name is a start-up failure,
not a page that silently reports one of them.

## Evaluation

1. **`Registry.Live`** — returns `Report{Status: StatusLive}` and runs nothing.
   [[D-090]] is why, and it is the single easiest thing in this flow to "fix"
   wrongly.
2. **`Registry.Ready` / `:Inspect`** — both call `evaluate`, which serves one
   shared pass:
   - a completed pass younger than `Freshness` is returned as it is, so a
     readiness endpoint scraped by every probe in the cluster does not become
     load on the dependency it asks about;
   - a pass already in flight is joined rather than duplicated;
   - the pass runs on `context.WithoutCancel(ctx)`. Waiters do not donate their
     cancellation to a flight other waiters are still in — the same rule as
     [[D-084]]. The flight is bounded by the per-check timeouts instead.
3. **`pass`** — runs every non-`Disabled` contribution concurrently, each in its
   own `context.WithTimeout`, writing into its own slot of a preallocated slice.
   `probing` recovers a panicking probe into an ordinary failed check.
   `Disabled` contributions are filled in as `StateDisabled` without being asked.
4. Status resolution, in `pass`: a failing `Required` check gives `StatusDown`; a
   failing `Degrading` check gives `StatusDegraded` unless something already gave
   `StatusDown`; `Informational` moves nothing. Only checks that moved the status
   *and* declared a `Code` contribute to `Report.Codes`, which is sorted.
5. `CheckDetail.Message` is the probe's error, truncated to `MaxMessageBytes` on
   a rune boundary — a driver error can be a paragraph.

## The two projections

- **`Report`** — status plus stable codes. Nothing derived from the deployment.
- **`Detail`** — names, codes, importances, states, messages, durations, and the
  observation time.

## The Fiber binding

`app/http/appfiber/health.go`:

1. **`Health(HealthSpec)`** — refuses a spec with no registry and a path that
   does not start with `/` or ends with one. `Operator` is the permission set the
   detail page needs; empty means the detail page is not mounted at all.
2. **`Mount`** — `GET {path}/live`, `GET {path}/ready`, and `GET {path}/detail`
   when there are operator permissions. **`Access`** declares the first two
   `Public` with the reason (a probe has no account) and the third `Requires`.
   Both halves are written next to each other because the boot gate in
   [[FL-024]] compares them and refuses to start on a disagreement.
3. **`detail`** — `permitted` calls `auth.Require` and `auth.HasAll`, and a
   refusal is rendered through `porthttp.Renderer` — 401 with no principal, 403
   without the permission, and in neither case any part of the detail.
4. **`statusFor`** — `StatusDown` is 503; everything else, `StatusDegraded`
   included, is 200. [[D-090]].

## Where the decisions bite

- Adding a dependency ping to `Live` restarts every replica whenever that
  dependency is slow. [[D-090]].
- Putting the check's name or the probe's message into `Report.Codes` turns the
  public endpoint into a map of the deployment. [[D-091]].
- Letting the caller's context reach the probe means one abandoned scrape fails
  the pass every other scraper is waiting on. [[D-084]].
- A `<subsystem>health` package is the combination package the extension
  architecture forbids; the adapter is two lines in the composition root.
  [[D-091]].

## Files

| File | What it holds |
|---|---|
| `health/health.go` | `Importance`, `Status`, `State`, `Probe`, `Contribution`, `Report`, `Detail`, `CheckDetail`, `MaxMessageBytes` |
| `health/registry.go` | `Spec`, `New`, `Auto`, `accept`, `RegistrationError`, `Live`, `Ready`, `Inspect`, `evaluate`, `pass`, `ask`, `probing` |
| `health/healthfx/healthfx.go` | `AsCheck`, `Registered`, `Spec`, `Checking`, `Auto` |
| `app/http/appfiber/health.go` | `HealthSpec`, `Health`, `healthRoute`, `permitted`, `statusFor` |

## Tests that walk this flow

`health/registry_test.go`, `health/healthfx/healthfx_test.go`,
`app/http/appfiber/health_test.go`.
