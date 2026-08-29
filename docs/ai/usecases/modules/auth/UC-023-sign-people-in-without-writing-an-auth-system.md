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
9. A failed sign-in says nothing about which half was wrong, and costs the same
   whether or not the identifier exists.
10. A caller can only close their own sessions. A session id belonging to
    somebody else answers as though it did not exist, rather than confirming
    that it does.
11. Choosing signed tokens instead is one declaration. It changes what a client
    receives and what a verifier checks, and changes nothing about how the
    author writes sign-up, sign-in or authorization.
12. With signed tokens, a credential used twice is detected. Two browser tabs
    refreshing at the same moment is not an error; a credential replayed after it
    was spent closes the session it belonged to.
13. Routes exist for a framework the author already uses, and choosing one does
    not drag in the others.

## Status

Covered. The session, credential and grant halves are in place with both an
opaque and a signed strategy; the identity-linking half — signing in through an
external provider — is not, and an author who needs it today writes their own
`Registrar` and calls the enrolment use case.
