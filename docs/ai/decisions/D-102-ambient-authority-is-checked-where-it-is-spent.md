# D-102 — Ambient authority is checked where it is spent

**Status:** accepted
**Invariant:** a cookie-delivering access surface refuses an unsafe request that
carries one of its own cookies, presents no header credential, and cannot say it
came from somewhere this deployment serves. The check never decides who the
caller is, and turning it off takes a written reason.

## The decision

`accesshttp.Credentials.Protect(method, header, cookie)` runs at the top of every
unsafe handler in `accessnet`, `accessgin` and `accessfiber`. It refuses with
`errs.KindForbidden` and `accesshttp.CodeCrossSite` unless one of these holds:

| | |
|---|---|
| the method is `GET`, `HEAD`, `OPTIONS` or `TRACE` | nothing changes |
| no cookie of this deployment's is on the request | there is no ambient authority to spend |
| an `Authorization` header is present | a header is not ambient |
| `Sec-Fetch-Site` is `same-origin` or `none` | the browser has already said where it came from |
| `Origin` is one of `CrossSite.Origins` | the front end this deployment is built for |

`CrossSite.Unsafely` is the waiver, and it takes a sentence rather than a `bool`.

## Why

**Because `SameSite` is not a CSRF defence, it is a delivery rule.** `Strict`
happens to prevent the attack by refusing to send the cookie; `Lax` sends it on
top-level navigation; `None` — which a front end on another origin requires — sends
it always. A deployment that switched to `None` to serve its SPA silently
switched the defence off, and nothing in the module said so.

**Because the thing that distinguishes the attack is not who the caller is.** The
request carries a perfectly valid session. Authentication answers correctly and
authorization answers correctly, and the operation still was not asked for by
anybody. So the check belongs beside the credential, not beside the identity: the
question is whether this request may spend authority the page that caused it
never had to read.

**Because a header credential is not ambient.** A page that can set
`Authorization` has already read something a cross-site page cannot. Checking a
bearer request would refuse native clients, command-line tools and every server
that talks to this API, all of which send no `Origin` — for no gain.

**Because the check has to be on by default.** A protector a consumer must
remember to install protects the deployments that did not need it. What a
deployment can do is say, in a sentence that stays in the config, that its CSRF
defence is somewhere else.

**Because `Origin` alone is not enough and `Sec-Fetch-Site` alone is not
universal.** `Sec-Fetch-Site` is set by the browser and cannot be forged from a
page, so it is read first; `Origin` covers the clients that do not send it, and
is compared against a list rather than reflected.

## What this does not do

It does not stop a script that is already running on the deployment's own origin:
that request is same-origin, and an XSS is a different failure with a different
answer (`HttpOnly`, [[D-075]]).

It does not protect a consumer's own routes. It runs on the access surface —
sign-in, sign-out, rotation, password, sessions — because that is the surface this
module owns. An application whose own writes are cookie-authenticated needs the
same rule in front of them, and the pieces are exported for exactly that.

It does not double as an authorization check. A request that passes carries no
more authority than it did before.

## What it forbids

- Do not check a request that presented no cookie of this deployment's, and do
  not check a safe method. Both would refuse callers who are not spending ambient
  authority, and a check that refuses the innocent is a check somebody waives.
- Do not turn the waiver into a `bool`. `Unsafely` holds the reason so a reader a
  year later can weigh it.
- Do not reflect the `Origin` header back as an allowance. The list is the
  deployment's, and reflecting it allows everybody.
- Do not put the origin, the cookie or the session id in the rendered message.
  The refusal names none of them; the origin travels in the wrapped error
  ([[D-044]], [[D-056]]).
- Do not weaken this to a token in a cookie the same page reads back. A
  double-submit token is a second mechanism to keep correct, and it buys nothing
  over `Sec-Fetch-Site` plus an origin list on a surface that already sets
  `HttpOnly`.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/http/accesshttp/crosssite.go` | `CrossSite`, `Credentials.Protect`, `CodeCrossSite`, the protector and its origin list |
| `auth/access/http/accesshttp/cookies.go` | `Cookies.CrossSite`, and where the protector is built |
| `auth/access/http/accessnet/accessnet.go`, `accessgin/accessgin.go`, `accessfiber/accessfiber.go` | `jar.protect`, and the call at the top of every unsafe handler |

## Proven by

- `TestACookieBorneWriteFromAnotherSiteIsRefused` and
  `TestNothingIsRefusedWhereThereIsNoAmbientCredentialToSpend` —
  `auth/access/http/accesshttp/crosssite_test.go`. The second is the control that
  makes the first mean something: a read, a request with no cookie of ours, and a
  request carrying a header credential all pass unrefused.
- `TestAnOriginTheDeploymentNamedIsAllowedAndAWaiverTurnsTheCheckOff` — the
  allow-list in both directions, and the waiver.
- `TestARefusalIsReachableWithoutReadingItsText` — the code a client and a log
  branch on.
- `TestACookieBorneWriteFromAnotherSiteIsRefusedByThisTransport` — carried
  file-for-file by `accessnet`, `accessgin` and `accessfiber`, each reading its
  own headers and its own cookie jar, and each asserting the handler refuses
  rather than the policy value alone.

## See also

[[D-075]] [[D-056]] [[D-044]] [[D-099]] [[FL-023]]
