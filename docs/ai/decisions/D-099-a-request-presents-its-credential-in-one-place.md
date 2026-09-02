# D-099 — A request presents its credential in one place

**Status:** accepted
**Invariant:** a guard reading a named cookie accepts a credential from the
cookie or from the `Authorization` header, never from both, and never from two
cookies of that name. Two sources are a refusal wrapping
`ErrCredentialCardinality`, not a ranking. The old precedence survives only as
`UnsafeCookieWinsOverAuthorization`.

## The decision

`authhttp.Cookie(name)` reads the cookie and the header, and answers:

| Cookie | `Authorization` | Result |
|---|---|---|
| one | absent | the cookie, as a `Bearer` credential |
| absent | present | the header, parsed |
| one | present | refused |
| two of that name | any | refused |

The refusal is `auth.AmbiguousCredential`, the same shape and sentinel the guard
already raises when one header arrives twice — one class of failure, one thing
to match on.

The lookup can refuse at all because `auth.LookupOrRefuse` exists: a source hook
that returns `(Credential, bool, error)`. `auth.Lookup` remains the ordinary
form and is a thin wrapper over it, so a lookup with nothing to refuse writes no
error.

## Why

**Because which of two credentials wins is not the request's to decide.** The
cookie is the browser's, and it is attached by the browser to any request the
page can cause. A script that cannot read an HttpOnly cookie can still make the
browser send it — so a page that deliberately presented a fresh bearer token
had its own credential silently replaced by whatever cookie was still in the
jar. That is a downgrade to another session, and it looked like a working
request.

**Because a stale cookie is exactly the shape that survives a sign-in.** [[D-075]]
already clears the cookie a body-delivered credential did not go into, for this
reason. A refusal is the honest end of the same argument: the two credentials
disagree about who is calling, and picking one is guessing.

**Because same-name cookies are ambiguous by construction.** A cookie set for
`.example.test` and one set for `api.example.test` both arrive under one header,
in an order the RFC leaves to the client. First-win means a path a deployment
never chose decides which session answers.

**Because the legacy behaviour has real deployments behind it.** Naming it
`Unsafe…` keeps the migration possible while making it impossible to select by
accident.

## What this rules out

- **Ranking sources.** No option orders them; the safe one refuses.
- **A silent downgrade to anonymous.** The lookup refuses even under
  `auth.Optional()`: an ambiguous credential is a bad credential, and
  [[FL-019]] already refuses those on an optional guard.
- **A per-source sentinel.** `ErrCredentialCardinality` covers "more than one
  credential reached the guard", whether that is one header twice or two
  different places.

## Where it lives

| File | What it holds |
|---|---|
| `auth/http/authhttp/cookie.go` | `Cookie`, `UnsafeCookieWinsOverAuthorization`, and the same-name count |
| `auth/guard.go` | `Lookout`, `LookupOrRefuse`, `Lookup` as the wrapper, and the refusal reaching the caller |
| `auth/errors.go` | `AmbiguousCredential` and the cardinality refusal built from it |

## Proven by

- `TestARequestPresentingTwoCredentialsIsRefusedRatherThanRanked` and
  `TestTheLegacyCookiePrecedenceIsAvailableOnlyByNamingIt` in
  `auth/http/authhttp/cookie_test.go`.
- `TestAListAwareGuardRefusesEveryDuplicateCredential` in
  `auth/guard_cardinality_test.go` — the same sentinel from the header side.

## See also

[[D-075]] [[D-076]] [[FL-019]] [[FL-023]] [[UC-019]]
