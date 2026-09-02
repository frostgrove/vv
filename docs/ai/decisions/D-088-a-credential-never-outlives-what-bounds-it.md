# D-088 — A credential never outlives what bounds it

**Status:** accepted
**Invariant:** an access token's `exp` is never later than the end of the session
it was minted from; a rotation honours the session idle deadline the opaque path
already honours; and a lifetime matrix in which one bound outlives another is
refused at start-up rather than served.

## The decision

Three rules, all in `accessjwt`, all about the same thing.

1. **The clamp.** `core.answer` mints `exp = min(now + AccessTTL,
   session.ExpiresAt)`. `sessions.expires_at` is absolute — no rotation moves it
   ([[FL-023]]) — so it is the only end a token has to respect.
2. **The idle deadline applies to rotation too.** `Classify` takes a `Window`
   holding both the grace window and the idle TTL, and `Presented` carries the
   session's `last_used_at`. A session nobody has touched for longer than
   `SessionConfig.IdleTTL` is `Unusable`, which is what
   `SessionAuthenticator.Authenticate` has always done through `Session.Live`.
3. **The matrix is checked once, at `Build`.** `AccessTTL > RefreshTTL`,
   `RefreshTTL > SessionConfig.TTL`, `AccessTTL > IdleTTL` and
   `RefreshGrace >= RefreshTTL` are refusals, not warnings.
4. **A lifetime left out and a lifetime written below zero are different
   answers.** Zero is the caller taking the default — `DefaultAccessTTL`, the
   session TTL, `DefaultRefreshGrace`. A negative duration is refused by name and
   by value, before any default is put in its place.

## Why

**Because a refresh a second before the session ends used to mint five more
minutes.** The absolute `expires_at` is the whole point of having one: it bounds
a session however active the caller is. Minting a token from it without looking
at it turned "thirty days, absolutely" into "thirty days, plus one `AccessTTL`
for anybody who refreshes at the right moment" — and with a deny-list absent,
those minutes are unrecallable ([[D-072]]).

**Because two strategies over one `sessions` table must not disagree about when a
session is over.** The opaque path refused an idle session at every request; the
rotating path refused it never, because `Classify` had never been told what idle
meant. A deployment that switched strategies changed a security property it did
not know it was configuring.

**Because the matrix is a deployment mistake, and [[D-021]] says where those
fail.** `AccessTTL: 24h` next to `RefreshTTL: 5m` is not a request that can be
refused sensibly at request time — every token minted under it is already wrong.
It is a configuration that must not start.

Making the third rule a start-up refusal is also what lets the first two stay
simple: because `AccessTTL <= IdleTTL` holds by construction, a freshly rotated
token (whose `last_used_at` is `now`) cannot outlive the idle deadline either, so
`answer` clamps against one end rather than two.

## What it forbids

- Do not mint a token from a lifetime without reading the session row's own end.
  A strategy that keeps its own copy of when a session dies has two answers to
  one question.
- Do not read the idle TTL from `Spec`. It is `SessionConfig.IdleTTL`, the same
  value the opaque path reads, precisely so the two cannot drift.
- Do not turn the matrix check into a warning or a clamp. A deployment that
  silently had its `AccessTTL` shortened at boot reports the value it configured
  and serves another.
- Do not treat a negative duration as "unset". It is the same clamp wearing a
  zero test: `AccessTTL: -5m` became five minutes, the matrix check then read the
  five minutes and agreed with itself, and the `-5m` somebody wrote was never
  mentioned by anything again.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/accessjwt/accessjwt.go` | `refuseLifetimesBelowZero` — a duration written below zero, refused before a default replaces it; `checkLifetimes` — the start-up refusal of the matrix; `core.answer` — the clamp; `core.window` |
| `auth/access/accessjwt/rotation.go` | `Window`, `Presented.LastUsedAt`, the idle arm of `Classify` |
| `auth/access/access.config.go` | `SessionConfig.TTL`, `IdleTTL` — the bounds both strategies read |
| `auth/access/access.model.go` | `Session.Live` — the same two deadlines on the opaque path |

## Proven by

`auth/access/accessjwt/accessjwt_test.go` —
`TestARefreshMintsNoAccessTokenThatOutlivesItsSession` (with the session-outlasts
control beside it), `TestARefreshRefusesASessionAbandonedPastTheIdleDeadline`,
`TestBuildRefusesALifetimeMatrixThatOutlivesTheSession` (with an admissible
control that must pass first), `TestTheDefaultLifetimeMatrixStartsUp` and
`TestALifetimeBelowZeroIsRefusedWhereAnOmittedOneTakesTheDefault`.

`auth/access/accessjwt/rotation_test.go` —
`TestASessionIdleLongerThanTheIdleTTLIsUnusableHoweverGoodItsCredential` and
`TestAnIdleDeadlineNobodySetLeavesEveryCredentialAlone`.

## See also

[[D-068]] [[D-072]] [[D-021]] [[FL-023]] [[UC-023]]
