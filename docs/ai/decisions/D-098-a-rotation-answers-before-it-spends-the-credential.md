# D-098 — A rotation answers before it spends the credential

**Status:** accepted
**Invariant:** the replacement credential and the access token exist before the
compare-and-swap that retires the presented one. A refresh that loses the swap
re-reads its session, classifies the credential it was given against what is
there now, and rotates again from the winner's digest instead of refusing.

## The decision

`core.rotate` mints the next credential, loads the grants and signs the access
token first, and only then runs the swap on `(id, token_hash, revoked_at IS
NULL)`. Nothing that can fail is left between the write and the answer, so a
failure to sign or to read grants leaves the presented credential exactly as
spendable as it was.

A swap that changes no row is a lost race and not a verdict. The row is read
again by id, the *presented* digest is classified against it, and `Rotate` or
`RotateAgain` means another refresh got there first: the loser swaps from the
winner's digest, and both callers leave with a usable credential — the winner's
own is the lineage's previous digest and stays usable for the grace window.
Anything else the re-read classifies as — `Replay`, `Unusable` — is the refusal
it always was. The loop is bounded at `rotationAttempts`; past that the answer
is a refusal, because a session being rewritten that many times in one instant
is not a client this code can serve.

## Why

**Because the loser of an honest race is a live client.** A browser that opens
two tabs, or a client that retries a request the network dropped, presents one
credential twice. The bare compare-and-swap answered one of them 401 — and a
401 on rotation is a sign-out, which is exactly the state a rotating credential
exists to avoid. The old comment said the second request "lands in the
previous-digest branch and is told apart there", and that is true only of a
*later* request that re-reads the row; two requests that both read before either
wrote never reach it.

**Because a write that outlives its answer spends a credential nobody received.**
Signing can fail on a misconfigured key and grants can fail on a database
hiccup. With the swap first, the row already names a replacement the caller
never saw: the client retries with what it holds, and only the grace window
stands between it and a `Replay` that closes the whole session.

**Because a transaction would not be enough and costs more.** The failing half
is the signing and the grants read; ordering them before the write removes the
window without holding a transaction open across them, and without asking every
consumer's `crud.Source` to support one where the endpoint opens none.

## What this rules out

- **Treating `changed == 0` as a refusal.** It is a question — what does the row
  say now? — and the answer decides.
- **Re-reading by the presented digest instead of by id.** After a concurrent
  rotation the digest is no longer the row's current one, and the lookup that
  found it is not the lookup that proves what happened to it.
- **An unbounded retry loop.** A row somebody rewrites in a tight loop must end
  as a refusal rather than as a request that never returns.
- **Reusing the winner's replacement for the loser.** Only its digest is stored;
  the credential itself is gone, so handing the loser "the same answer" is not
  something this can do.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/accessjwt/accessjwt.go` | `core.rotate`, `core.swap`, `core.reread` and `rotationAttempts` |
| `auth/access/accessjwt/rotation.go` | `Classify` — the pure verdict both the first read and the re-read use |

## Proven by

- `TestARefreshThatLosesTheRaceRotatesAgainRatherThanSigningTheCallerOut` and
  `TestARotationMovesTheLineageOnlyOnceTheReplacementExists` in
  `auth/access/accessjwt/rotation_race_test.go`.

## See also

[[D-088]] [[D-072]] [[FL-023]] [[UC-023]]
