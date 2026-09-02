# D-075 — Where a credential goes is the request's choice, except on rotation

**Status:** accepted
**Invariant:** A caller names one of three deliveries in `X-Auth-Delivery`, and
gets exactly the one it named or a refusal. Silence takes the most closed
delivery the deployment offers. A credential that goes into a cookie leaves the
body, and a credential that goes into the body clears the cookie it did not go
into. On rotation the caller does not choose where the rotating credential goes:
it goes back through the channel it arrived on.

## The decision

One session has two credentials with opposite lifetimes: an access token that
lives minutes and a rotating credential that lives weeks. Where each of them
should go depends on what is calling, and one deployment has more than one kind
of caller. So it is a property of the request:

| `X-Auth-Delivery` | access token | rotating credential | who asks |
|---|---|---|---|
| `cookies` | HttpOnly cookie | HttpOnly cookie | a browser |
| `refresh-cookie` | body | HttpOnly cookie | a page that sends its own header |
| `body` | body | body | a native client, a command-line tool |

There is no fourth. The combination it would be — the access token in a cookie
and the rotating credential in the body — puts the durable half where a script
can read it and the short-lived half where it cannot, which is the arrangement
backwards.

**A header and not a field in the body**, because of the sign-up endpoint. Its
body is the application's own type ([[D-066]]), so nothing in the library can add
a field to it without a wrapper the consumer writes per payload; and a rotation
by cookie has no body at all. A header is the same two lines on all three
endpoints in all three bindings.

**Silence takes the most closed delivery.** Where cookies are configured that is
both credentials in them. A browser that forgets to ask is still a browser that
cannot read its own credentials; a native client that forgets finds an empty
body and no session, which is a failure somebody sees in the first minute.

**A value nobody defined is refused, not defaulted.** `invalid_enum`, which the
standard vocabulary renders as 422.

**A deployment that configured no cookies refuses to deliver one** rather than
answering in the body, because answering in the body hands the credential to the
page of a caller who asked for it to be kept away from one.

**A half delivered to the body clears the cookie it did not go into.** Without
that, a browser signing in again as a body caller keeps the previous session's
access cookie for the rest of its five minutes — and it would then send both
that cookie and the header it just filled, which the guard refuses as two
credentials ([[D-099]]). Before that refusal existed the cookie quietly won, and
the page went on acting as the session it had just replaced; either way the
clearing is what keeps the caller working.

**Rotation is not a choice.** `accesshttp.Rotating` forces the rotating half to
the channel the presented credential arrived on. What the request still decides
there is the access token, which is the whole difference between `cookies` and
`refresh-cookie` for a browser.

## Why

**Because the cookie's only purpose is that a script cannot read it, and an
endpoint that hands it back in a body gives that away for free.** A script
injected into the page cannot read an HttpOnly cookie, but it can make the
browser send one. If the rotation endpoint honoured "give it back in the body",
the script would ask, and walk away with a credential good for weeks from its
own machine. Everything else here is arrangeable per request; this one cannot be,
because the party asking is not necessarily the party the credential belongs to.

**Because the alternative to a request-level choice is a deployment-level one,
and a deployment has more than one kind of caller.** A configured "cookies or
body" makes the browser and the mobile client of the same product mutually
exclusive, and the second one arrives after the choice is in production.

**Because the delivery cannot be inferred.** A User-Agent is a string the caller
chose; the presence of an Origin header is a fact about the request and not about
where the caller wants its credentials. Guessing means a native client that
happens to look like a browser gets cookies it will not store, and its session
ends at the first rotation.

**Because a guard that reads a cookie is what makes the closed delivery usable at
all.** Both credentials in cookies means no Authorization header, so
`authhttp.Cookie(name)` exists and falls back to the header — a deployment
serving a browser and a native client runs one guard for both. One request still
uses one of the two: presenting both is a refusal ([[D-099]]).

## What this rules out

- **A default of `body`.** It is the delivery a caller can read, so silence would
  mean the safest client is the one that remembered to configure itself.
- **Reading the delivery out of the sign-in body.** It would work for two of the
  three endpoints and force a wrapper type on every consumer for the third.
- **Honouring a delivery on rotation.** See above; it is the one refusal in this
  document that is not about ergonomics.
- **A configurable cookie name.** It is derived from the subject's prefix, so two
  kinds of caller on one host cannot overwrite each other's access cookie — which
  is what a shared name would do, since the access cookie is scoped to the whole
  API rather than to one endpoint.
- **HttpOnly as an option.** A credential cookie a script can read is what this
  is for avoiding, and an option to turn it off is an option somebody would find.

## Proven by

- `auth/access/http/accesshttp/delivery_test.go` — silence takes the most closed
  delivery, an undefined value is refused rather than defaulted, a cookie asked
  of a body-only surface is refused, rotation answers through the channel the
  credential arrived on, and no delivery is the fourth combination.
- `auth/access/http/accesshttp/cookies_test.go` — a credential in a cookie is not
  also in the body, a body-delivered half clears its cookie, each cookie is
  scoped to what spends it, two subjects do not share a name, and SameSite=None
  without Secure refuses to start.
- `auth/access/http/access{net,gin,fiber}/*_test.go` — the delivery header is
  read and the cookie is written by each transport, with the attributes intact.
- `auth/http/authhttp/cookie_test.go` — the guard reads the cookie, still reads
  the Authorization header, does not take another cookie's value, and refuses a
  request that presents both ([[D-099]]).

## See also

[[FL-023]] · [[UC-023]] · [[D-066]] · [[D-045]] · [[D-055]] · [[D-099]]
