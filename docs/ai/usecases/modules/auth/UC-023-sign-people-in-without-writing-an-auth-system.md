# UC-023 — Sign people in without writing an auth system

**Actor:** the application author, building a product that has accounts
**Covered by:** [[FL-023]] [[FL-019]] [[FL-020]]

## Scenario
The author has a product with accounts and needs the whole ordinary apparatus:
sign up, sign in, sign out, sign out everywhere, change your password, see the
devices you are signed in on, and an administrator who can grant roles and reset
somebody's password. None of it is the product. All of it is where the
embarrassing failures live — an account reachable under two spellings of the
same address, a sign-out that does not sign anybody out, a demotion that takes
effect in an hour.

They do not want a framework's idea of what a user is. Their account row has
their columns: a company, a phone number, a locale, an invitation code. A
library that owned that table would be a library they outgrow in a month.

Later they add a second kind of caller — internal services, or staff on a
separate portal — and the two must not collide: not on routes, not on the
address somebody uses in both, and not on which credential a route accepts.

Some of those callers may hold a signed token instead, because a service at the
edge has to verify without reaching the database. That is a change to one
declaration, not to the sign-in code.

## What must hold

1. What the author must supply is a value the compiler checks, not a procedure
   they have to find written down. Leaving out a required piece fails the build;
   leaving out an optional one has a stated default.
2. The account table is the author's. The library never creates a row in it, and
   adding a field to the sign-up form is an edit to the author's own type and
   nowhere else.
3. The account row and the credential that signs into it commit together. A
   duplicate refused by the author's own unique index leaves no half-registered
   account behind.
4. What an identifier *means* is the author's. Whether "Ann@Example.com" and
   "ann@example.com" are one account is a rule they state once, and it is applied
   to both the write and the lookup — never to one of them.
5. Two kinds of caller can hold the same identifier without either having to
   rename. A sign-in says which kind it is for, and that never comes from the
   request body.
6. Two kinds of caller mount without colliding. Registering the same kind twice,
   or two kinds on the same path, fails at start-up rather than at run time.
7. Signing out closes the session, and the next request from it is refused.
   Signing out everywhere closes the rest and reports how many. Deactivating an
   account locks it out on the next request, not when its credential expires.
8. Roles and permissions are read from stored rows on every request, so taking a
   role away takes effect on the next call.
9. What a registration grants is data, changed on a running system and
   inspectable afterwards, and it can be wrong out loud: naming a role that does
   not exist is refused when it is set, not weeks later at somebody's first
   sign-up. Naming none is a supported state, not a misconfiguration, and it
   grants nothing.
10. A failed sign-in says nothing about which half was wrong, and costs the same
    whether or not the identifier exists — including when the identifier or the
    password is longer than anything the deployment accepts, which is refused
    before it is hashed rather than after.
11. Guessing is bounded. A deployment can cap attempts per identifier and per
    address without the library learning what a Redis is, can watch what is
    refused, and cannot be brought down by the cost of its own password hashing:
    the number of hashes running at once has a ceiling, and work past it is
    refused as busy rather than queued without limit.
12. An account has one password. Resetting it ends every way of signing into that
    account with the old one — there is no second identifier left working, and a
    reset that would leave one is refused rather than reported as done.
13. A caller can only close their own sessions. A session id belonging to
    somebody else answers as though it did not exist, rather than confirming
    that it does.
14. Choosing signed tokens instead is one declaration. It changes what a client
    receives and what a verifier checks, and changes nothing about how the
    author writes sign-up, sign-in or authorization.
15. With signed tokens, a credential used twice is detected. Two browser tabs
    refreshing at the same moment is not an error — including when both of them
    read the session at the same instant, where the one that loses the race
    leaves with a working credential rather than a sign-out; a credential
    replayed after it was spent closes the session it belonged to. A rotation
    that cannot be answered spends nothing: the credential the caller holds still
    works.
16. Routes exist for a framework the author already uses, and choosing one does
    not drag in the others.
17. One deployment serves a browser and a native client without either of them
    holding a credential the other's threat model forbids. Where a session's two
    credentials go is said per request — both in HttpOnly cookies, the rotating
    one alone, or both in the body — a request that says nothing gets the most
    closed of those, and a request that says something unknown is refused rather
    than quietly given the default. The one exception is rotation: the rotating
    credential comes back through the channel it arrived on, because a script on
    the page can make the browser send a cookie it cannot read, and an endpoint
    that honoured "put it in the body" would hand that script a durable
    credential. See [[D-075]].
18. A signed token names the service it was minted for, and a verifier requires
    that name. Two services that share a signing key do not share each other's
    sessions, and a deployment that wants a token no service claims says so with
    a name that reads as unsafe.
19. A stored password hash is data like any other. A row nothing wrote correctly
    is an unreadable credential — not a wrong password, and never a process that
    stops.
20. A write made with a cookie the browser attached by itself says where it came
    from. A deployment that delivers credentials in cookies refuses an unsafe
    request that carries one of its own cookies, presents no header credential
    and can name neither a same-origin fetch nor an origin the deployment
    listed. A read, a request with no such cookie and a request carrying a header
    credential are not asked, and a deployment whose CSRF defence is elsewhere
    turns the check off by writing down why.
21. What is listed as an active session is one that could still be used. A row
    past its expiry or its idle deadline is not shown to its owner as somewhere
    they are signed in, and the clock that decides it is the one that issued it.
22. A revocation the issuing strategy could not be told about is not lost. The
    sign-out still succeeds, the failure is logged, and it can be replayed from
    the rows afterwards — telling a deny-list twice costs nothing, and a
    credential that has expired anyway is left out.
23. A session's stated end is its real end. However a caller refreshes, no
    credential they hold verifies past the moment the session expires or past the
    idle deadline the deployment configured, and a configuration in which one
    lifetime outlives another does not start.
24. A lifetime the deployment did not write and a lifetime it wrote wrongly get
    different answers. Leaving one out takes the documented default; writing a
    duration below zero stops the start and names the field and the value, rather
    than becoming that default and being reported nowhere.
25. A deny-list that could be emptied behind the deployment's back does not
    start. A revocation store held on a server configured to discard keys under
    memory pressure is refused at start-up, naming what that server is set to
    do — a discarded revocation reads exactly like a session nobody closed. A
    server that will not say how it behaves is a third answer rather than a
    passing one: it is reported by default and a deployment can ask to be
    refused instead, while a store that answers nothing at all is refused
    whatever the deployment asked for.

## Status

Covered. The session, credential and grant halves are in place with both an
opaque and a signed strategy, and a browser can hold both credentials where no
script reaches them; the identity-linking half — signing in through an external
provider — is not, and an author who needs it today writes their own `Registrar`
and calls the enrolment use case.
