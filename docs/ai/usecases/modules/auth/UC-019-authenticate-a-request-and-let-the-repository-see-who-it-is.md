# UC-019 — Authenticate a request and let the repository see who it is

**Actor:** the application author, on behalf of every caller of the service
**Covered by:** [[FL-019]] [[FL-007]] [[FL-008]] [[FL-011]] [[FL-013]]

## Scenario
Requests arrive carrying a token, or an API key, or nothing. The author wants
the caller's identity established once, at the edge, and then available to every
rule further in — the row filter, the coarse check, the per-row check — without
threading a parameter through four layers or inventing a context key per
service.

Two things make this harder than it looks. The identity has to reach the
*repository*, not just the handler, because that is where the rules live; and it
has to be the same identity on every transport, because a service that serves
both HTTP and gRPC must not have two answers to "who is calling".

And the author wants the token parsing to be theirs to shape. Their tokens have
a tenant claim spelled the way their identity provider spells it, and a library
that insisted on its own claims struct would be a conversion step on every
request.

## What must hold

1. The rule is declared once, at the edge. Nothing at a call site changes, and
   no handler reads a header.
2. The identity reaches the repository. A rule declared next to the repository
   can read it, on every transport, without the transport and the repository
   agreeing on anything the library did not already define.
3. A request that presents no credential is refused before the handler runs,
   and the refusal is the transport's own "unauthenticated" answer — 401 over
   HTTP, `UNAUTHENTICATED` over gRPC.
4. A request that presents a credential that does not verify is refused
   identically. Absent, expired, forged, wrong audience and valid-but-unknown
   are one answer to the client.
5. The refusal names no reason. A client cannot tell which check failed, and
   cannot learn whether a subject exists.
6. The reason is still recoverable inside the process, for a log — and it is
   *reported* at one seam rather than left to each transport: an observer
   registered on the guard sees every refusal, with a kind, a sentence the
   library wrote and the error, and never the credential itself.
7. An endpoint may be declared optional, meaning a request with no credential
   proceeds with no identity. A *bad* credential is still refused there: a token
   that does not verify never silently becomes anonymous.
8. One guard instance authenticates once per request however many times that
   instance is installed. Mounting it globally and again on a group costs one
   verification; mounting a different guard performs that guard's own check and
   cannot inherit the first guard's result.
9. The claims type is the author's. A parser can be pointed at their own struct,
   and nothing of this library's appears in it.
10. Turning claims into an identity is a separate, explicit step. An author who
    wants only the parser never takes the rest.
11. That step may refuse. A token that verified but names something the
    application will not accept — a deleted tenant — is refused as an
    authentication failure, with the same silent body.
12. More than one kind of credential can be accepted at one endpoint, and the
    refusal does not reveal how many were tried or which ones exist.
13. A signing algorithm is decided by the key the deployment configured, never
    by the token. A token that nominates its own verification scheme is refused.
14. A configuration that would over-trust fails at start-up, not at request
    time — and every relaxation of it has a name that appears at the call site.
15. Everything above is the same on `net/http`, Gin, Fiber and gRPC. The same
    guard object drives all four.
16. A request presents its credential in one place. The same source carrying two
    values, a cookie beside an `Authorization` header, or two cookies of the
    configured name are all refused rather than ranked — an optional endpoint
    included, because an ambiguous credential is a bad credential and not an
    absent one.
17. A refusal a transport writes carries every header it was rendered with. A
    401 that offers two authentication schemes offers both, on every binding.
18. A browser's CORS preflight is not refused for presenting no credential.
    Ordering a CORS middleware in front of the door does it where the chain is
    the author's to order; where it is not, each HTTP binding has a decorator
    that recognises a preflight by its one shape — `OPTIONS`, an origin, a
    requested method and no credential — and nothing else. An `OPTIONS` carrying
    a credential is authenticated like any other request.
19. What a preflight is granted is an answer, not a route. The decorator hands
    it to the handler the author named for preflights, or answers it itself when
    nobody was named, and the request ends there — no handler of the
    application's own runs for a request that skipped the door. The shape the
    decorator recognises is written by whoever sends the request, so anything
    reachable past it is reachable by anyone.

## Out of scope

- **Issuing anything.** No login, no refresh, no signing, no user store. This
  reads what was presented.
- **Deciding what the caller may do.** That is UC-020. Establishing identity and
  authorizing an operation are separate, and an authenticated caller with no
  permissions is a normal state.
- **Session cookies, CSRF and rate limiting.** Different subsystems; none of
  them is what a repository rule needs. Limiting *sign-in attempts* belongs to
  the subsystem that owns sign-in and is part of [[UC-023]]; this use case is
  about a credential already in hand.
- **mTLS.** A principal derived from a client certificate is an authenticator
  the application writes; the rest of this use case then applies unchanged.
- **Revoking a token before it expires.** A denylist is application state, and
  the place for it is the step in guarantee 10.
- **Re-checking a credential mid-stream.** A gRPC stream is authenticated when
  it opens; a long-lived stream that must re-check does so itself.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-019]] | the credential leaving the request, becoming a principal, and entering the context |
| [[FL-007]] | the principal being read by a scoped read |
| [[FL-008]] | the principal being read by a scoped write |
| [[FL-011]] | the refusal becoming a status |
| [[FL-013]] | what differs between the four transports, and what does not |

## Status
**covered.**

Every guarantee is pinned. Guarantees 3, 4, 5, 7 and 8 are carried file-for-file
by the three HTTP bindings and in the gRPC vocabulary by the fourth, so a
divergence between transports fails a test rather than being discovered in
production. Guarantee 8 has both controls: the same guard verifies once and two
different guards each verify. Guarantee 5 carries a control case that asserts the leak *is* there
when the reason is put where it would obviously go, so the positive assertion
cannot quietly stop meaning anything. Guarantee 13 has a test that isolates it
from the underlying library's own key typing, because the first version of that
test passed with the pinning removed.

Two things are worth stating rather than leaving to be found:

**An optional guard in front of a gated repository is not an open door.** It
lets an anonymous request past the middleware, and every policy in the security
decorator then refuses an absent principal — so the 401 arrives from the
repository instead of from the edge. That is the intended composition and it is
what guarantee 7 means, but the status comes from further in than a reader might
expect.

**Guarantee 6's reason is not reachable with `errors.Unwrap`.** A fault unwraps
to a slice, which the single-error form does not walk. `errors.As` down to the
fault and then `Unwrap` is the way in.

The idempotence marker is deliberately not the principal. A principal proves
that some guard authenticated the request; only the marker for one concrete
guard proves that guard did so ([[D-076]]).
