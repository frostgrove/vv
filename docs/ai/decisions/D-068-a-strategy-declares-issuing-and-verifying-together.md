# D-068 — A strategy declares issuing and verifying together, and a guard is per subject

**Status:** accepted
**Invariant:** One `access.Strategy` value produces both the `SessionIssuer` and
the `auth.Authenticator` for one subject. A guard is mounted per subject's route
group and refuses a credential belonging to another subject type. The verifier
set is closed by what was declared, never by trying every format the library can
parse.

## The decision

How a caller holds a session — an opaque token, a signed one, a signed one with
a revocation list — is one declaration in `SubjectSpec.Strategy`, and it answers
for both ends. `Strategy.Build` returns an `Issued{Issuer, Authenticator,
Refresher}`.

Each mounted subject exposes its own `Guard()`. A binding puts it in front of
that subject's routes, so the route group a request arrived on selects the
verifier before anything is parsed. Routes not under any subject's prefix — the
roles, permissions and grants endpoints — use `Runtime.AdminGuard()`, a chain
over the **declared** strategies.

Both `access.SessionAuthenticator` and `accessjwt`'s verifier check the subject
type and refuse a credential that names another.

## Why

**Because issuing and verifying have to agree and are edited apart.** They share
a format, a key and a subject type. Wired as two independent objects they agree
until somebody changes one, and the failure is either everybody signed out at
once or — worse and quieter — a token minted for one subject type accepted as
another.

**Because a global guard cannot express two formats.** With one guard for the
whole API, a deployment where users hold JWTs and internal services hold opaque
tokens has to try both on every request: an opaque lookup for every JWT that
arrives, and a 401 that means "none of several parsers liked this". Owning the
mount lets the factory put the right verifier on the right group, so one
declared strategy means one verifier and a refusal that can say what was wrong.

**Because the closed set is the point.** Trying every format the library knows
is an attack surface and a latency cost that nobody asked for. What a consumer
declared is what runs.

**Because a prefixed route is a security boundary.** `/staff/auth/…` behind a
guard that accepted any valid session would authenticate a customer as a staff
caller. The credential is genuine, the signature verifies, nothing looks wrong —
the caller is simply somebody other than the route assumed. The subject-type
check lives in the authenticator, so no binding can forget it.

## What it forbids

- Do not return an `Authenticator` from one place and an `Issuer` from another.
  If a strategy grows a third end, it goes on `Issued`.
- Do not mount one guard over every subject's routes when more than one strategy
  is declared.
- Do not build the admin chain from anything but the mounted subjects.
- Do not drop the subject-type check from an authenticator, and do not move it
  into a handler: a handler that forgets it fails open.
- Do not add a fallback that tries an undeclared format.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/access.strategy.go` | `Strategy`, `StrategyDeps`, `Issued`, `SessionIssuer`, `SessionRefresher`, `OpaqueToken` |
| `auth/access/access.runtime.go` | `Mount`, `MountedSubject.Guard`, `Runtime.AdminGuard` |
| `auth/access/access.authenticator.go` | `SessionAuthenticator.For`, and the refusal for another subject type |
| `auth/access/accessjwt/authenticator.go` | the same check against the `sty` claim |

## Proven by

- `access.TestMountRegistersAWellFormedSubject` asserts a mounted subject leaves
  with both an issuer and a verifier; the three refusal tests beside it are the
  ones a broken `Mount` would trip.
- `access.TestMountRefusesTwoSubjectsUnderOnePrefix` — two guards on one group
  is the shape this forbids.
