# D-103 — A preflight is answered, never routed

**Status:** accepted
**Invariant:** a request that skipped the guard because it looks like a CORS
preflight never reaches a route. The wrapper hands it to the handler the
consumer named for preflights and ends the request there; with nobody named it
answers `authhttp.PreflightStatus` (`204`) itself. No binding calls `Next` on a
skipped preflight.

## The decision

`authhttp.Preflight` stays what it is: a predicate over `OPTIONS`, an `Origin`,
an `Access-Control-Request-Method` and no `Authorization`. What changes is what
a binding is allowed to do with a match.

| Wrapper | A preflight | Everything else |
|---|---|---|
| `AnswerPreflight(middleware, preflight)` | goes to `preflight`, which answers it; the chain ends | goes through the guard |
| `SkipPreflight(middleware)` | is answered `204` with no `Access-Control-Allow-*` header | goes through the guard |

`SkipPreflight` is `AnswerPreflight` with the framework's own answer, so the
one-statement mount survives and the explicit form is underneath it. On Gin the
wrapper calls `Abort` after the answer returns, because a Gin middleware that
merely returns lets the route run; on Fiber and net/http not calling `Next` is
enough. A nil answer panics at construction, like a nil guard.

## Why

**Because both headers the predicate reads are the client's to set.** `Origin`
and `Access-Control-Request-Method` are forbidden headers *for a browser*, which
is a rule about browsers and not about anybody else. `curl` sets them. The old
wrapper called `Next` on a match, so those two headers were enough to carry an
unauthenticated `OPTIONS` past a globally mounted door into whatever handler the
router had for that path — and the binding's own test asserted that handler ran.

**Because a hand-written `OPTIONS` is surface.** [[D-073]] and the boot access
gate treat an `OPTIONS` route somebody wrote as a route like any other, which
has to declare its access. Declaration is an audit, not enforcement, so a route
whose only check is the global guard had none at all for the one method a
forgeable pair of headers could reach.

**Because the preflight never needed the route.** What answers a preflight is a
CORS middleware, and a CORS middleware terminates it — it writes
`Access-Control-Allow-*` and returns `204` without continuing. Passing the
request further down the chain was never part of answering it; it was how the
answer was *reached* when the consumer mounted CORS behind the door. Naming that
handler is the same wiring made explicit, and it costs one argument.

**Because the argument-free mount had to survive.** H-AUTHHTTP-02's must-hold 3
refuses an exemption mechanism, and one-statement mounting is H-AUTHHTTP-01's.
`SkipPreflight` keeps both. Its `204` with no CORS headers is a browser-visible
CORS failure for a consumer who never said who answers CORS — the same failure
they would get from a missing CORS middleware, and not an open door.

## What this rules out

- **A path list.** The argument is a handler, not a route pattern. There is
  still nothing to point at a path, and [[UC-019]]'s must-hold 3 is intact.
- **A preflight reaching application code.** Not "unless the handler checks" —
  the wrapper does not call `Next` at all.
- **A CORS answer invented by this library.** The bare `204` carries no
  `Access-Control-Allow-*` header, because the policy is the consumer's and a
  guessed one is worse than a visible failure.

## Where it lives

| File | What it holds |
|---|---|
| `auth/http/authhttp/preflight.go` | `Preflight`, `HeaderOrigin`, `HeaderRequestMethod`, `PreflightStatus` |
| `auth/http/authnet/authnet.go` | `AnswerPreflight`, `SkipPreflight` over `func(http.Handler) http.Handler` |
| `auth/http/authgin/authgin.go` | the same, plus the `Abort` that stops Gin's chain |
| `auth/http/authfiber/authfiber.go` | the same, over `fiber.Handler` |

## Proven by

- `TestACorsPreflightIsAnsweredByTheHandlerNamedForItAndABareOptionsIsNot` —
  `authnet`, `authgin`, `authfiber`. The preflight arm asserts the named handler
  answered *and* that the mounted `OPTIONS` route did not run; the controls are a
  bare `OPTIONS` (401) and an `OPTIONS` carrying a credential (authenticated, and
  the route does run).
- `TestAPreflightNobodyAnsweredStopsAtTheDoorInsteadOfAtTheRoute` — the same
  three, over `SkipPreflight`: forged headers, `204`, and no handler.
- `TestAPreflightAnswerThatIsNotThereRefusesToStart` — the same three, and
  `TestATypedNilPreflightAnswerIsStillNothingToAnswerWith` in `authnet`'s
  `binding_test.go`, where the answer is an interface and can be a typed nil.

## See also

[[D-073]] [[D-100]] [[FL-019]] [[UC-019]]
