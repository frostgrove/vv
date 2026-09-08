# D-120 — A refresh credential names the generation that minted it

**Status:** accepted
**Invariant:** a refresh credential is `<generation>.<session id>.<random>`. Reuse
is detected however far back the credential comes from: a presented generation
below the row's current one minus one is `Replay`, closes the session and denies
its outstanding access token. The random part is still the only thing that
authenticates; the two prefixes only decide which row to look at.

## The decision

### What was wrong

A session row carries two digests — `token_hash` and `previous_token_hash` — and
the lookup tried them in that order. That is one rotation of history, and it is
all the reuse detection there was.

So the theft response in [[D-098]]'s neighbourhood covered exactly one case: a
credential presented while it was still the *previous* one. Present a credential
from three rotations ago and it matched neither column, `find` returned nothing,
and `Refresh` answered a plain 401. Nothing was closed, nothing was denied, no
warning was logged, and the legitimate holder went on refreshing.

The incentive that creates is backwards. A thief who spends a stolen credential
immediately trips the replay arm and loses the session for both of them. A thief
who waits for two more rotations — minutes, on a five-minute access TTL — gets a
401 that tells the victim nothing, and can keep probing. The window in which
theft is *detectable* was the grace window plus one rotation, and the window in
which the credential is *useful to a thief* is unrelated to it.

### What the credential carries now

`mintCredential(generation, session)` returns

    <generation>.<session id>.<random>

and the session row carries `generation`, incremented by the same swap that moves
the digests. `Refresh` tries the two digests as before; when neither matches, it
reads the row the credential names and asks whether that row has moved past the
generation the credential claims. If it has by more than one, `Classify` answers
`Replay`, and the ordinary theft response runs: the session is closed with
`ReasonRefreshReplayed`, the deny-list gets the session id, and the warning is
logged.

Only "by more than one" is a replay. A credential naming the current or the
previous generation would have matched a digest, so arriving here with one means
it did not match — and something that names a generation but no digest of it is a
refusal, not evidence of anything.

### Why the prefixes are safe to add

Neither prefix is a secret and neither authenticates. The digest still does. A
credential naming a session it did not come from matches no stored digest, and
the superseded lookup will not close a session on the strength of a generation
number alone — the row has to be genuinely ahead of what the credential claims,
which means that credential's generation really was issued and really was spent.

Forging one to close somebody else's session needs their session id, which is a
v4 UUID that was never shown to anybody but them and the server. That is the same
secret the deny-list and the `sid` claim already turn on.

### Upgrading a running deployment

A credential minted before this existed carries no prefix. `credentialPrefix`
answers `ok == false` for it, `generationOf` answers 0, and `Classify` skips the
new case on `presented.Generation > 0`. Such a credential falls back to exactly
the two-digest lookup it was minted under, so the deployment does not sign
everyone out on the deploy: outstanding credentials keep working until their
first rotation, and every rotation mints a prefixed one.

The column is added by the same migration, `NOT NULL DEFAULT 0`, so rows written
before the upgrade read as generation 0 and cannot be ahead of anything.

## What it forbids

- Do not treat either prefix as authentication. The digest comparison is what
  decides that a credential is real; a prefix decides only which row to read.
- Do not classify a credential as `Replay` on a generation the row has merely
  reached. It has to be *past* it by more than one, because the current and the
  previous generation both have a digest and would have been found by it.
- Do not make the prefix required. A credential without one is a credential from
  before this decision, not a forgery, and refusing it is a mass sign-out.
- Do not reuse the session id prefix as a lookup key for anything that does not
  then verify a digest.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/accessjwt/accessjwt.go` | `mintCredential`, `credentialPrefix`, `generationOf`, `core.findSuperseded`, `presentedOf` |
| `auth/access/accessjwt/rotation.go` | `Presented.Generation`, `Presented.CurrentGeneration` and the `Classify` case that reads them |
| `auth/access/accessjwt/model.go` | `rotatingSession.Generation` |
| `auth/access/accessjwt/migrations/00001_accessjwt.sql` | the `generation` column |

## Proven by

- `TestACredentialOlderThanThePreviousRotationIsStillAReplay` in
  `auth/access/accessjwt/rotation_race_test.go` — a credential from three
  rotations back closes the session and reaches the deny-list. Deleting either
  half of the fix — the `findSuperseded` fallback or the `Classify` case — fails
  it.
- `TestACredentialNamingASessionItNeverCameFromClosesNothing` in the same file —
  the control: a credential that matches no digest and names a session only one
  generation ahead writes nothing at all, so the test above is not passing on the
  session id alone.
- `TestAReplayedRefreshCredentialClosesTheSessionAndDeniesItsAccessToken` — the
  one-rotation-back case, unchanged by this.

## See also

[[D-098]] [[D-088]] [[D-112]] [[FL-023]] [[UC-023]]
