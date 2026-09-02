# D-097 — An access token names the service it was minted for

**Status:** accepted
**Invariant:** every access token `accessjwt` mints carries an `aud`, and its
verifier requires that exact value. A deployment that names no audience gets its
own issuer as one. A token with no audience at all exists only under
`UnsafeAnyAudience`, and a spec that both names an audience and waives every
audience refuses to start.

## The decision

`Spec.Audience` is what the token is minted for and what the parser demands
back. When it is empty the strategy fills it with `Spec.Issuer`: a self-issued
session names the service that issued it, which is the answer for the single
deployment and is never "anybody".

`Spec.UnsafeAnyAudience` is the one way to a token that names nobody. It mints
no `aud` and verifies any, it is spelled with the risk in its name, and it
cannot be combined with `Spec.Audience` — a spec carrying both is refused by
`Build`, because the two say opposite things about the same claim and whichever
won would be a silent choice.

`Claims` deliberately does not carry `aud`. The audience is a verification
input, not something a caller reads out of the struct, and a `string` field
would fail to decode the JSON-array form that is equally legal — turning a
policy check into a payload-shape refusal.

## Why

**Because one signing key is normally shared by more than one service.** An
issuer that mints for the billing API with the same HMAC secret the reporting
API verifies has, without an audience, handed every billing session a reporting
session. Nothing about that is visible: both tokens verify, both name a real
subject, and the mistake is a configuration file two teams copied.

**Because `authjwt` already forces the choice and `accessjwt` was making it.**
`authjwt.New` panics unless it is given `Audience(...)` or
`AllowAnyAudience()`. The access strategy answered that question once, for
every consumer, with the unsafe arm — and no consumer could see it had been
asked.

**Because a default of "the issuer" costs a deployment nothing.** It is the
value a single-service deployment would have typed, so the safe path needs no
declaration, and a consumer who has two services types the one they mean.

## What this rules out

- **Minting without `aud` because the verifier happens to be lenient.** The
  waiver is the only lenient verifier, and it says so at the call site.
- **A default of "any audience".** Silence takes the closed answer here, as it
  does for the credential delivery in [[D-075]].
- **Reading the audience out of the token to decide what to check.** The
  deployment decides; the token never widens it ([[D-078]]).

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/accessjwt/accessjwt.go` | `Spec.Audience`, `Spec.UnsafeAnyAudience`, the issuer fallback, the contradiction refused by `Build`, the `aud` claim in `core.answer` and the parser's `authjwt.Audience` |

## Proven by

- `TestAnAccessTokenIsRefusedByAServiceItWasNotMintedFor`,
  `TestAnAudienceNobodyNamedIsTheIssuerRatherThanNone` and
  `TestAnAudienceIsWaivedOnlyByNamingTheRisk` in
  `auth/access/accessjwt/audience_test.go`.

## See also

[[D-078]] [[D-088]] [[FL-023]] [[UC-023]]
