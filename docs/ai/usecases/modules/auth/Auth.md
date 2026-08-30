# auth · authjwt · apikey — establish who is calling, once, so every rule further in can ask

**Covers:** `github.com/frostgrove/vv/auth`, `github.com/frostgrove/vv/auth/authjwt`, `github.com/frostgrove/vv/auth/apikey`
**Not covered by this sweep:** `github.com/frostgrove/vv/auth/access` and its satellites. That subtree is a working implementation of these contracts rather than one of them, and its own scenario is [[UC-023]]; nothing below has been re-run against it.
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — several happy-path/DX gaps below still need their own work, including credential forwarding. The corrected security seams now re-run stricter guards, distinguish provider outage, bound JWKS retirement, provide boot-time JWKS warming, validate and snapshot static key declarations, refuse nil-like extensions and identities, and snapshot both live Guard configuration and fixed API-key Claims.

## What a consumer is actually trying to do

Somebody has a service with rows in it that belong to different people, and
requests arriving with a token. They want the token turned into an identity once,
at the edge, and they want that identity to be the thing the row filter reads —
not a header their handler copies into a struct and threads down four call
frames. The whole reason they are here rather than writing twenty lines of
middleware is that twenty lines of middleware do not reach the repository.

They arrive with an identity provider they did not choose. Auth0, Keycloak,
Cognito, an in-house issuer — it signs with RS256, publishes a key set, and
spells the tenant `org`, or `tenant_id`, or nests it under `https://myapp/claims`.
It spells roles differently too. They want to write down what their issuer means
once, and never think about JWT libraries again. Some of them have no key set at
all: an internal issuer that hands out one public key in a config file or a
Kubernetes secret, and a PEM they have to turn into something before any of this
starts.

A good share of them arrive with no identity provider at all, and this is the
first thing they need told. There is no login here, no refresh, no signer, no
user store — this reads what was presented and nothing mints it ([[UC-019]] Out
of scope). A team with email and password in a table hits that boundary in the
first hour, and the answer is that issuing is their application's job. Another
share arrive with identity they already have: a gateway that terminated mTLS and
forwarded a header, a session store, a `User` type ten thousand lines of their
code already uses. For them the whole ask is that the thing they have becomes
the thing the repository reads.

Very often the token is not where the answer lives. It carries a user id and
nothing else, because roles change on a Tuesday and nobody wants to wait for a
token to expire. So "who is calling" ends in their own database, on the request
path, every request — and they want to know whether that is the shape this
library expects and what it costs.

They also have machine callers. A nightly export, a webhook receiver, a partner
integration — things that will not do an OAuth flow and want a key in a header.
Those keys have to be revocable on a Tuesday afternoon without a deploy, which
means they live in a table, not in a config file. And in a tenanted service a
partner key is not a caller until it names a tenant, the same as everybody else.

Then there is more than one service. The handler that just learned who is
calling calls the next service, which wants a credential — and the identity is
on the context but the thing that proves it is not. And there is more than one
transport: REST for the browser, gRPC for the internal callers, one process, one
answer to "who is calling".

Then it goes to production, and what they want changes. They want to turn
authentication on for a fleet of live clients without finding out in the incident
channel how many of them were wrong. They want to know why the 401s started at
14:20. They want the key rotation their security team scheduled not to log
everybody out. They want an outage at the identity provider to look like an
outage and not like every customer's credentials going bad at once. They want a
server whose clock drifted four seconds not to be an incident. This second set of
wants is where a library either has been in production or has not.

And they want to be able to test any of it without standing up an identity
provider in CI.

## Happy cases

### H-AUTH-01 — Wire a hosted identity provider and have the tenant reach the SQL
**Who:** the author of a multi-tenant SaaS, on day one of adding auth
**Wants:** requests carrying an Auth0 token to see only their own tenant's rows, without a handler doing anything about it.
**Story:** They paste the JWKS URL, the issuer and the audience from the provider's dashboard, build a guard, mount it, and add a scope to the repository naming the claim their issuer uses for the tenant. They do not write a handler.
**Must hold:**
1. The verification key comes from the provider's published key set — nothing is pasted into the config by hand.
2. A token that nominates its own signing algorithm is refused before any key is consulted.
3. An integer tenant claim reaches the `WHERE` clause as an integer, including one above 2^53.
4. A consumer who copies the documented key-set snippet against a real provider gets a verified token on the first request, and the repository sees the tenant.

**Today:** 🟡 partial — 1 and 2 hold and are pinned; 3 is proven for half its journey; 4 is proven nowhere.
**Evidence:** Key set: `auth/authjwt/jwks.go:40`. Method pinning: `auth/authjwt/parser.go:127` (`jwt.WithValidMethods`), proven by `TestAnRSAParserRefusesAnHMACTokenSignedWithItsPublicKey` (`auth/authjwt/parser_test.go:81`) and `TestOnlyTheMethodsTheKeyDeclaresAreAccepted` (`:219`). For 3, `TestALargeIntegerClaimSurvivesAtAnyDepth` (`auth/authjwt/claims_test.go:146`) proves the claim comes back from `Attr` as an `int64` at any depth and stops there; nothing carries a value above 2^53 into a statement — `test/integration/auth_jwt_test.go` seeds `tenant: 1` and `2`, and `grep -rn 9007199254740 test/` is empty. For 4: `grep -rn JWKS _examples/ test/` returns one hit, and it is a comment (`_examples/auth-jwt-gin/main.go:118`). Both the runnable example (`:139`) and the three-engine integration fixture (`test/integration/auth_jwt_test.go:59`) wire `authjwt.HMAC` — a secret in the source, which is the opposite of guarantee 1. The JWKS path is proven only against an `httptest` server in `auth/authjwt/jwks_test.go`, and it is the path that touches the network, holds a cache, and carries three of this document's blockers.
**If not ready:** nothing to write — the code is there and the unit tests are good. What is missing is a demonstration: one `_examples/auth-jwt-jwks` against a local key-set server, and one arm on the integration test carrying a tenant above 2^53. Whether the narrowing then applies to every entry point the repository has is [[UC-004]]'s guarantee and the security sweep's proof, not this one's.

### H-AUTH-02 — An internal tool, one shared secret, roles in the token
**Who:** the author of an admin console used by eleven people
**Wants:** HS256 against a secret from the environment, a roles claim, and rules written against permissions rather than role names.
**Story:** They read a secret from the environment, declare a role map next to the policy, and pass both to one constructor. The permission names in the policy are the ones the rules use; the role names live only in the map.
**Must hold:**
1. No claims struct to declare for an ordinary token.
2. Roles expand to permissions once, when the principal is built, so the same token means the same thing everywhere in the process.
3. The claim names the ready-made type reads are stated somewhere a consumer reads before deploying, and what happens to the ones it does not read is stated with them.

**Today:** 🟡 partial — 1 and 2 are right and pinned; 3 is nowhere.
**Evidence:** `Standard` is both calls (`auth/authjwt/authenticator.go:50`), pinned by `TestStandardIsTheTwoCallsInOne` (`auth/authjwt/claims_test.go:104`). Expansion once: `auth/principal.go:92` and `auth/authjwt/claims.go:137`, pinned by `TestGrantFoldsTheRoleMapInOnce` (`auth/principal_test.go:38`) — and [[D-055]] forbids resolving a role at check time, so this is decided rather than incidental. For 3, the type reads `roles` and only `roles` (`auth/authjwt/claims.go:24`), `permissions` and the OAuth `scope` string (`:29`, folded into permissions at `:142-146`, read again by `Has` at `:117`). Three consequences, none of them written down anywhere a consumer looks:
- A roles claim spelled any other way yields no role-derived permissions. Keycloak nests them under `realm_access.roles`, Cognito sends `cognito:groups` — `grep -rn 'realm_access\|cognito' auth/` is empty. Microsoft Entra is the exception that proves the point: its app-role claim *is* spelled `roles`, so it works, and its delegated-scope claim `scp` is read by nothing (`grep -rn scp auth/` is empty) — a third spelling of permissions the ready-made type misses.
- Whatever the token's `scope` happens to carry becomes the permission vocabulary. Any OIDC access token carries `openid profile email`, so the principal holds three permissions that look real and match no policy. That is worse than holding none: a debugger shows a populated principal and every `RequirePermission` still refuses.
- A refusal from that has no reason on the wire and none in a log (H-AUTH-17), on every request, from the first deploy.
**If not ready:** for those issuers the short path is not the short path — they drop to `New[C]` plus a mapper on day one, which is H-AUTH-13's cliff arriving before anything unusual has been asked for. The library's own comment concedes the point and answers "write a mapper" (`auth/authjwt/authenticator.go:13`). Two cheap closures, and they are not the same: one paragraph in `docs/modules/en/authjwt.md` listing the four claim names `Claims` reads, saying that a nested or renamed one needs the mapper, and saying that `scope` is taken as permissions verbatim — documentation, no code; or a `Roles` field on the mapper-free path, which is a design question this sweep does not need to settle. The documentation half should not wait for the other.

### H-AUTH-03 — The tenant claim is nested, or namespaced
**Who:** the same author, whose provider spells the tenant `{"https://myapp/claims": {"tenant": 42}}`
**Wants:** to name that claim in a scope, the way a flat one is named.
**Story:** They read the tenant out of the token in a debugger, see the value, and look for how to say "the tenant is under this namespace".
**Must hold:**
1. A claim the token carries can be named by a policy, whatever shape the issuer chose.
2. The convenience form that exists for a flat claim has a counterpart for a nested one, or the module page names the route.
3. Whatever a consumer writes for it fails closed: an absent claim is a denial, never an empty value.

**Today:** 🟡 partial — 1 is reachable and undocumented; 2 and 3 are missing.
**Evidence:** `narrow` goes to deliberate trouble to preserve an `int64` *inside* nested maps and slices, and says why (`auth/authjwt/claims.go:76-86`). Reaching them is a four-line closure and does not leave `Standard`: `security.ScopeField[M, ID](field string, value func(context.Context) (any, error))` is exported (`crud/decorators/security/policies.go:31`) and `ScopeAttr` is nothing but `ScopeField[M, ID](field, attrOf(attr))` (`crud/decorators/security/principal.go:143`), so an extractor that calls `auth.Require`, does `p.Attr("https://myapp/claims")` and one map index gets the same reconcile-to-column-type treatment `ScopeAttr` gets. What is missing is that this appears in no document, that there is no `ScopeAttrPath`, and that the closure carries an obligation nothing states: `attrOf` turns an absent claim into a denial on purpose, because a missing tenant read as zero compiles to `WHERE tenant_id = 0`, which matches no rows on most schemas and every row on one where 0 is real (`crud/decorators/security/principal.go:169-186`). The obvious hand-written version — `v, _ := p.Attr(ns); return v.(map[string]any)["tenant"], nil` — reintroduces exactly that, and panics on a nil map on the request that has no claim.
**If not ready:** the route today is that closure, and nobody has written it down. Closing it is `security.ScopeAttrPath(field string, path ...string)` — the walk runs over the `map[string]any` `Attr` already returns, which is why `narrow` preserves nested `int64`s, and it reuses `attrOf`'s denial. **It should not be a dotted name on `Attr`.** `Principal.Attr` is one of four methods every consumer implements over their own type (H-AUTH-04), so a dotted `Attr` would be understood by `authjwt.Claims` and `auth.Claims` and not by a hand-written `User.Attr`, and the difference would surface as a permanent silent narrowing failure that depends on who wrote the principal. The path form keeps the whole change out of `auth`, which matters for a frozen surface. The panic a mismatched claim type produces at the first request is the security sweep's blocker 5, not a second one here — **count it once.**

### H-AUTH-04 — The service already knows who the caller is
**Who:** a team behind a gateway that terminates mTLS, or with a session store, or with a `User` type ten thousand lines of their code already uses
**Wants:** their existing identity to become the thing the repository's rules read, without adopting a second identity type.
**Story:** They implement the four-method interface over their own type, write a five-line authenticator that resolves the forwarded header to one, and mount the same guard everyone else mounts.
**Must hold:**
1. No type of this library's appears in their identity type.
2. A credential this library has never heard of is a supported case, not a fork.
3. Whatever they implement, a role still means the same permissions everywhere in the process.

**Today:** 🟡 partial — 1 and 2 are exactly what the interface is for; 3 is stated as a rule and is unenforceable for anyone who takes 1 at its word.
**Evidence:** `Principal` is an interface and `Claims` is one implementation of it, which `auth/doc.go:37-40` names as a refusal rather than an accident. `auth.AuthenticatorFunc` (`auth/credential.go:42`) plus `auth.Lookup` (`auth/guard.go:60`) covers 2, and it is the seam the module page points mTLS at. For 3: `Grant` exists on `auth.Claims` (`auth/principal.go:92`) and on `authjwt.Claims` (`auth/authjwt/claims.go:137`) and nowhere else. A consumer implementing `Principal` over their own `User` writes `Has` themselves, and the obvious `Has` walks their role map at check time — which is precisely the "same token means two things in one process" bug `auth/principal.go:88-91` and [[D-055]]'s last forbid exist to prevent. Nothing panics, nothing tests it, and `docs/modules/en/auth.md`'s "expansion happens once" quietly stops holding for every consumer who implements the interface.
**If not ready:** the code they write is right or wrong by luck. Closing it costs a paragraph in `docs/modules/en/auth.md` under `Principal` — expand when you build it, and `RoleMap.Expand` is exported for exactly that (`auth/principal.go:108`) — and it is the one obligation in this module that the type system cannot carry. A shipped `auth.From(subject, roles, perms, attrs, m)` helper would make the right shape the shorter one, which is the only enforcement available here.

### H-AUTH-05 — API keys for machine callers, held hashed in the database
**Who:** the author of a public API with partner integrations, in a tenanted service
**Wants:** issue a key, store its hash, authenticate against it, have the partner see their own rows, and revoke by deleting a row.
**Story:** They write a lookup that hashes the presented key and selects the row. They follow the module page and put the authenticator behind an `X-Api-Key` header. Every partner job comes back 401, and nothing on either side says why.
**Must hold:**
1. A key nobody issued is a 401; a lookup that could not run is not.
2. Deleting the row takes effect on the next request, with no restart and no cache to bust.
3. The wiring the module pages show works against a client that sends the key the ordinary way — the bare value, no scheme word in front of it.
4. The documentation names who generates a key and shows it, not only who hashes it.
5. A machine principal can name a tenant, so the flagship row filter applies to it.
6. A busy endpoint does not become one database round trip per request for identity alone.

**Today:** 🟡 partial — 1, 2 and 3 hold; 4 is half done; 5 and 6 have nothing.
**Evidence:** The three-result `Store` is the seam and it is right: `auth/apikey/apikey.go:40`, the outage pass-through at `:104-107`, pinned by `TestAStoreFailureIsNotARefusal` (`auth/apikey/apikey_test.go:83`). Revocation needs no cache to bust because there is no cache: `Lookup` is the only path to a principal (`:103`).

For 3: `apikey.Header("X-Api-Key")` is the dedicated bare-value option. It uses
`auth.Lookup` to synthesise `Credential{Scheme: DefaultScheme, Token: key}` and
therefore makes `X-Api-Key: k-batch-1` work without changing `auth.Header`'s
scheme-shaped grammar (`auth/apikey/apikey.go:84-104`).
`TestHeaderReadsABareKeyWithoutChangingTheAuthorizationAPI`
(`auth/apikey/apikey_test.go`) walks the complete Guard → authenticator → store
path. Its control proves the helper replaced Authorization rather than passing
vacuously, and a third arm proves `Authorization: ApiKey k-batch-1` still works.
The English and Russian module pages now use the bare helper in their canonical
wiring and state the distinction next to it ([[D-076]]).

For 4, the hashing half is documented well, in a code block, with the constant-time requirement attached (`docs/modules/en/apikey.md:64-82`). The generation half — `crypto/rand` plus an encoding — is nowhere. For 5, both shipped examples of a hand-built machine principal set `Sub` and `Permissions` and no `Attrs` (`docs/modules/en/apikey.md:29-33`, `docs/modules/en/auth.md:54-59`), and the flagship policy is `ScopeAttr("TenantID", "tenant")` — so a partner key copied from the page authenticates and is then denied on every request by `attrOf` ("the caller carries no tenant claim", `crud/decorators/security/principal.go:183`), with the reason dropped on the wire. Nothing found for 6.

**Obligation rather than guarantee:** constant-time comparison. `apikey.Static` compares every entry with `crypto/subtle` and explains why (`auth/apikey/apikey.go:114-125`, `:140`), but the case's own story is a database, where the timing property belongs entirely to the consumer's SQL. The package states the requirement — index by the hash of the presented key, because the hash is what travels (`auth/apikey/apikey.go:20-22`) — and cannot enforce it for a `Store` somebody else writes. It is documented as a requirement and should stay described that way, not counted as something the library keeps.

**If not ready:** For 4, five lines of `crypto/rand` and base64 appended to the section that already shows the hash. For 5, one line in the `Static` example giving the partner an `Attrs` map. For 6 they write a caching `Store` wrapper, and that is the sharp edge: **any cache of hits trades guarantee 2 away, and the trade is the whole design.** A cached hit is served without consulting the store, so a deleted row keeps working until the entry expires — caching *misses* cannot do that, it only delays recognising a key that was just issued. So `apikey.Cache(store, ttl)` has to state the number it costs ("revocation now takes up to `ttl`") and offer the invalidation call for the Tuesday afternoon, and it must key on the hash and never on the presented key, or the wrapper is a plaintext key store in process memory and undoes the discipline `Store` exists to enforce.

### H-AUTH-06 — One endpoint, a user's JWT and a service's key
**Who:** the author of an API called both by a browser app and by their own batch fleet
**Wants:** `/exports` to accept either credential.
**Story:** They write a lookup that prefers `X-Api-Key` and falls back to the bearer token, and chain the two authenticators.
**Must hold:**
1. The refusal when neither works says nothing about how many kinds exist.
2. If the key store is down, the caller gets a 500 and not a 401 — whichever order the authenticators were wired in.

**Today:** 🟡 partial — 2 is the best-tested decision in the subsystem; 1 is decided and unpinned.
**Evidence:** `auth.Chain` at `auth/credential.go:96`; `auth.Lookup` at `auth/guard.go:60`, with the two-credential example in its own doc comment. Guarantee 2 is what the code went out of its way for — the first non-refusal wins over any refusal, `auth/credential.go:113` and `:117`, pinned by `TestAnOutageAnywhereInAChainBeatsARefusal` (`auth/guard_test.go:150`). The wiring order is not something a consumer has to get right to keep an outage visible, and the comment says so. Guarantee 1 is a [[D-056]] forbid ("do not report which authenticator in an `auth.Chain` refused, or how many were tried") and it does hold, because the reason never renders — but the subtest that reads like its pin asserts the sentinel and nothing about disclosure (`auth/credential_test.go:81-86`), which is the same defect this sweep raises as blocker 8 for two other tests. By this document's own standard a name is not an assertion.
**If not ready:** one subtest asserting that the chain's error text names neither member. One edge, not a gap: `authjwt`'s authenticator ignores the credential's scheme entirely (`auth/authjwt/authenticator.go:29` uses only `cred.Token`), so in `Chain(jwt, keys)` every API key is first parsed as a JWT and refused. `apikey` guards the mirror image deliberately and says why (`auth/apikey/apikey.go:69-77`), and the asymmetry is worth an `authjwt` scheme check for symmetry, not for safety. The route-table half of this case is [[FL-013]]'s and the authhttp sweep's; what is auth's is the outage rule above.

### H-AUTH-07 — The provider rotates a key, revokes it, and then has a bad afternoon
**Who:** the on-call engineer, at 03:00, reading the provider's status page
**Wants:** a rotation to be invisible, a revocation to take effect, and an outage to look like an outage.
**Story:** The provider publishes a new key and starts signing with it. Later, after an incident, it withdraws the old key from the published set. Later still it returns 503 for twenty minutes. Nothing about the service is redeployed.
**Must hold:**
1. A token signed with a key added since start-up verifies, without a restart.
2. A stream of tokens naming keys that do not exist does not become a stream of requests to the provider.
3. A key the provider has withdrawn stops being accepted within a maximum age the deployment sets, measured from the last successful fetch, without a restart.
4. While the provider is unreachable, a caller holding a good token is not told its credentials are bad.

**Today:** ✅ handled.
**Evidence:** 1 remains pinned by `TestARotatedKidIsPickedUp`. 2 reads the last
attempt, shares one in-flight fetch, and preserves a failed attempt's typed
result for rate-limited callers; `TestUnknownKidsDoNotBecomeOneFetchEach`,
`TestAFailingProviderIsStillOnlyFetchedOnce` and
`TestAConcurrentBurstOfMissesIsOneFetch` pin the three branches. The failed
singleflight also returns one typed cause to every concurrent waiter, and
separate recorded-state bits keep both the success age and failed-attempt limit
correct when a deterministic clock starts at `time.Time{}`. 3 is now the
safe default: `JWKSFreshness` bounds cache age at five minutes,
`JWKSStaleAfter` changes it, and a hit at the boundary refreshes the whole map;
`TestARetiredCachedKidStopsVerifyingAtTheFreshnessBoundary` uses a fake clock
and mutable server to prove the removed key fails while its replacement works.
A method mismatch for a cached `kid` is an immediate refusal only while that
policy is fresh; at the boundary it refreshes too, so a same-`kid` RS256→PS256
rotation works and a failed refresh stays `ErrKeySourceUnavailable` rather than
becoming a verdict from stale metadata.
4 is split from credential failure by `ErrKeySourceUnavailable`, pinned cold by
`TestAKeySetThatCannotBeReachedIsUnavailableAndNotARefusal` and warm by
`TestAStaleCacheDoesNotHideAnOutageByDefault`. The optional availability policy
is finite and observable: `JWKSServeStaleFor` requires a typed degraded observer,
and `TestStaleOnErrorIsBoundedAndSignalsTheDegradedDecision` proves stale works
inside the window and unavailable wins at its exact end. Detached provider
timeouts and transport failures have the same stale semantics, while an
initiator or waiter cancelling only stops its own wait and receives its exact
context error. Observer delivery begins only after the flight releases waiters;
it is serialised/coalesced and its panic, re-entry or failure to honour the
one-second context cannot poison authentication. `Warm` refuses stale readiness
during the same outage even when request traffic has explicitly bought the
stale window. [[D-078]] binds the whole policy.
**One shape that remains deliberate:** a token with no `kid` matches only a set
holding exactly one key; anything else would choose a trust anchor on the
caller's behalf. A present malformed `kid` never enters that branch. Provider
entries themselves always need non-empty, unique ids, a verification operation
when `key_ops` is present, and one exact method: EC from `crv`, Ed25519 as EdDSA,
and RSA from mandatory `alg`. Explicit JSON `null` or empty policy values are
present and unusable, not aliases for omission. Static and remote RSA share the 2048..16384-bit
odd/composite/coprime checks; static and remote Ed25519 share canonical point
decoding and low-order rejection. `TestJWKSMethodsAndOperationsBelongToEachKey`
includes the cross-method EC control that the dependency itself accepts, and
`TestLowOrderEd25519TrustWouldAcceptAUniversalJWTForgery` proves why a 32-byte
length check is not key validation.

### H-AUTH-08 — Accept two issuers for a month, and two audiences during a rename
**Who:** the author moving off a legacy in-house issuer, with a month of overlap
**Wants:** both issuers accepted while the migration runs, without turning the issuer check off.
**Story:** They point the new provider at the same API, run both, and cut over when the legacy issuer stops minting. The same month, the API is renamed and the provider starts minting the new audience.
**Must hold:**
1. Two issuers can be accepted at once, with each token still checked against a named issuer.
2. Two audiences can be accepted at once, the same way, and the option that looks like it does that does not mean the opposite.
3. Doing either does not require the author to learn the underlying JWT library.
4. Two issuers minting the same subject do not collide into one identity.

**Today:** 🟡 partial — 1 and 2 are reachable only through a chain, 3 fails on the other route, 4 has nothing and is the dangerous one.
**Evidence:** `Issuer` takes exactly one issuer (`auth/authjwt/parser.go:40`) and is passed straight to golang-jwt (`:135`), which holds one expected value — so no key source can widen it. `Audience` is variadic and means *all of*, not *any of* (`:50`, pinned by `TestNamingTwoAudiencesRequiresBothOfThem` at `auth/authjwt/parser_test.go:251`), so naming two narrows rather than widens — the option a consumer would autocomplete into for guarantee 2 does the opposite of what they want, and its own comment says a deployment meaning "either" declares two parsers. The one-word reach for both is a waiver, not a widening: `AllowAnyIssuer()` (`:70`) and `AllowAnyAudience()` (`:80`), and the latter's own comment prices it — "an unaudienced token is replayable against every other service that trusts the same issuer". For 4: `Standard` maps `sub` straight through to `Principal.Subject()` (`auth/authjwt/claims.go:137-154`), and `security.ScopeSubject` narrows rows by it — so two issuers minting `u-1042` are one user to every policy in the process. `auth/authjwt/authenticator.go:14` names the fix ("a subject needs a prefix to be unique across two issuers") and `Standard` has nowhere to apply it, which is H-AUTH-13's gap arriving inside the case that most needs it.
**If not ready:** `auth.Chain` of two parsers works and is honest — each keeps its own issuer and audience check — and costs double verification, plus, with two JWKS sources, each provider's key set polled by the other's traffic, bounded to one fetch per minute per source. `authjwt.Custom` does **not** help here: it replaces the key, and the issuer check is independent of the key source. Closing it is a distinct option with its own validator — `AnyIssuerOf(iss ...string)`, checking the decoded `iss` after parse, and `AnyAudienceOf` beside it — and not a variadic `Issuer`: calling golang-jwt's option twice assigns rather than accumulates, which is the exact bug `TestNamingTwoAudiencesRequiresBothOfThem` exists to record, so that test is the control case any implementation has to pass. It is not purely additive either — `New` panics unless `s.issuer != "" || s.anyIssuer` (`auth/authjwt/parser.go:111`), so the start-up check becomes "named, or any-of, or waived" in the same change, and the waiver stays a distinct name because turning the check off and accepting two named issuers are different decisions. Guarantee 4 needs no code at all if the chain is the recommendation: it needs a sentence saying that the two parsers must map to prefixed subjects, next to the recommendation, because today the advice and the collision are in different documents.

### H-AUTH-09 — Replace the shared secret without a window where half the fleet refuses
**Who:** the same author, later, doing what their security team scheduled
**Wants:** publish the new secret, accept both, stop signing with the old, drop it.
**Story:** Two deploys, and every token minted in between verifies against one of the two secrets.
**Must hold:**
1. Two signing secrets can be accepted at once, for as long as the rollover takes.
2. Doing so does not put the JWT library back into the consumer's imports.

**Today:** 🟡 partial
**Evidence:** `authjwt.HMAC` takes exactly one secret (`auth/authjwt/key.go:44`).
**If not ready:** `authjwt.Custom` with a keyfunc returning a `jwt.VerificationKeySet` works, and puts `golang-jwt` back into the consumer's imports, which is the one thing this module exists to avoid. `auth.Chain` of two parsers also works and doubles verification. The parser side is a genuine pass-through — golang-jwt already accepts a key set from a keyfunc — but "small and additive" is two claims and only one of them is true. **A variadic `HMAC(secrets ...[]byte)` needs a panic of its own**: `KeySource.valid()` checks only that a keyfunc is non-nil and the method list is non-empty (`auth/authjwt/key.go:92`), and `HMAC` sets the methods unconditionally (`:44-50`), so `HMAC()` with no secrets passes `valid()`, `New` starts, and every token is refused for a reason nothing reports — the mirror-case misconfiguration blocker 6 exists to argue against. And widening the variadic keeps ordinary call sites compiling while breaking any use of `authjwt.HMAC` as a function value and changing a line in `docs/api/surface.md`, which this repository treats as a question for a person. The additive-in-every-sense form is a second constructor, `HMACAny(secrets ...[]byte)`.

### H-AUTH-10 — The clocks disagree by four seconds
**Who:** whoever is on call the week the NTP config changed
**Wants:** a token minted one second ago not to be "not yet valid".
**Story:** Intermittent 401s from one client fleet, on requests that worked a minute earlier, with no pattern anyone can see.
**Must hold:**
1. There is a knob for it, and it is named at the call site rather than inferred.
2. It widens the window by what it says and no more.
3. The canonical wiring a consumer copies includes it, or says why it does not.

**Today:** 🟡 partial — 1 and 2 hold; 3 is a documentation gap with a production incident on the other end of it.
**Evidence:** `Leeway(d)` (`auth/authjwt/parser.go:64`), pinned by `TestLeewayWidensTheWindowAndOnlyByWhatItSays` (`auth/authjwt/parser_test.go:139`). The doc comment is unusually good: it says the default is none, that thirty seconds is the usual production setting, and that `iat` is deliberately not checked. The repository's own runnable example sets thirty seconds (`_examples/auth-jwt-gin/main.go:142`) — and no module page's canonical snippet carries it.
**If not ready:** they find it after the incident. The fix is one line in the snippets that consumers copy, and it costs nothing. Intermittent skew 401s are indistinguishable from every other 401 by design ([[D-056]]), which is what makes this the cheapest incident on the list to prevent and one of the most expensive to diagnose (H-AUTH-17).

### H-AUTH-11 — Find out at boot that the auth configuration is wrong
**Who:** whoever is watching the deploy
**Wants:** a misconfigured key-set URL, issuer or audience to fail the deploy rather than the traffic.
**Story:** They ship a JWKS URL with a doubled letter. The process starts, the readiness probe passes, and the first request discovers the provider failure.
**Must hold:**
1. A configuration that cannot possibly authenticate anybody does not start.
2. There is some point before the first request at which a consumer can find out.

**Today:** ✅ handled for configuration the parser can validate.
**Evidence:** The declarations that make a parser over-trust fail at
construction, and `JWKS` panics on an empty URL. `Parser.Warm(ctx)` is the
explicit pre-traffic seam: for JWKS it performs the same bounded singleflight
fetch and document/key validation as `Parse`, returning
`ErrKeySourceUnavailable`; for already validated static sources it is a no-op.
`TestWarmFetchesAndValidatesJWKSBeforeTraffic` pins healthy, invalid-document,
static and caller-cancellation controls and asserts that `Warm` followed by
`Parse` costs one fetch. A non-empty but semantically wrong issuer or audience
cannot be inferred from a bare JWKS document; a deployment that needs that
external assertion checks discovery/configuration separately rather than
manufacturing a token in the parser.

### H-AUTH-12 — A public route, and a stricter check on `/admin`
**Who:** anyone with a health check and an admin area
**Wants:** an optional guard on the catalogue, and a step-up token — different audience, shorter life — on the admin subtree.
**Story:** They mark one guard optional and mount it. For the admin group they build a second, stricter guard and mount it inside the first. Both compile. Every admin request passes.
**Must hold:**
1. An optional endpoint lets an anonymous request through, and still refuses a credential that was presented and does not verify.
2. A second guard mounted inside the first verifies what it was built to verify, or says it cannot.
3. Mounting one guard globally and again on a group costs one verification.

**Today:** 🟡 partial — 2 and 3 hold. 1 holds for a well-formed credential and not for a malformed one.
**Evidence:** For 1: `Guard.Authenticate` still distinguishes absent from a
well-formed credential that its authenticator refuses, while
`ParseAuthorization` cannot distinguish an absent header from a present bare
token. `TestAnOptionalGuardStillRefusesABadCredential` pins the well-formed arm.

For 2 and 3, a successful guard adds its concrete `*Guard` and the principal
state it installed to an immutable marker chain. A consecutive repeat of the
latest instance is skipped; another instance reaches its own authenticator.
Re-entering A after B refuses with `ErrAmbiguousGuardOrder`: auth cannot infer
whether B was a step-up or a downgrade, so it retains neither answer silently.
`TestASecondGuardDoesNotAuthenticateAgain` and
`TestADifferentGuardAuthenticatesAgain` are the A -> A and A -> B controls;
`TestAReenteredGuardAfterAnotherIdentityBoundaryFailsClosed` pins both
`ordinary -> step-up -> ordinary` and `strict -> weak -> strict`. Unary and the
three HTTP bindings carry A -> A/A -> B; dedicated Stream tests carry all three
states on gRPC's replaced-context path ([[D-076]], [[UC-019]] guarantee 8).

**If not ready:** For guarantee 1, the malformed-credential arm still needs a
decision rather than a patch: refusing a schemeless header on an optional route
means `ParseAuthorization` distinguishing "absent" from "present and unusable",
which is a third result it does not have.

### H-AUTH-13 — Sign out everywhere, before the tokens expire
**Who:** the author implementing a "sign out of all devices" button, or responding to a compromised account
**Wants:** a token that verified to be refused anyway, because the session behind it is gone.
**Story:** They keep revoked session ids in Redis. On every request, after the signature checks out, they look the session up and refuse if it is listed.
**Must hold:**
1. There is a hook that runs after verification and may refuse.
2. A refusal there is the same 401 as any other — the client cannot tell a revoked session from a bad signature.
3. A Redis outage in that hook is a 500, not a 401.
4. Adding the hook does not mean rewriting the day-one wiring.

**Today:** 🟡 partial — 1 holds and is pinned; 2 and 3 hold and are pinned nowhere; 4 fails.
**Evidence:** The hook is the mapper (`auth/authjwt/authenticator.go:22`), and `TestAMapperMayRefuseATokenThatVerified` (`auth/authjwt/claims_test.go:122`) asserts only that an error came back — so 2 and 3 would both still pass if the refusal rendered as a 500 or carried its reason to the client. The mirror case in the neighbouring package does assert the classification (`auth/apikey/apikey_test.go:83`), which is the standard this one should be held to. 3 follows from the mapper's error travelling unchanged unless built with `auth.Unauthenticated` (`auth/credential.go:113` decides it inside a `Chain`).
**If not ready:** guarantee 4 is the cost. `Standard` — the path every author starts on — has no mapper argument, so a denylist means `New[authjwt.Claims]` plus `Authenticator` plus writing `c.Grant(roles)` by hand: **5 lines and 5 library names become 11 lines and 11** — `Standard`, `HMAC`, `RoleMap`, `Issuer`, `Audience` become `New`, `Claims`, `HMAC`, `Issuer`, `Audience`, `Authenticator`, `auth.Principal`, `auth.Unauthenticated`, `Grant`, `RoleMap`, `context.Context` — and the author must newly work out that `Standard` is `New[Claims]` plus a mapper, that the mapper's second result is what rejects, that `Grant` is what expands roles, and that the error has to be built with `auth.Unauthenticated` or it becomes a 500. That is the one place in this module where reaching further means starting over. The hook does **not** belong on `Standard` as an `Option`: `Option` is `func(*settings)` and the same type is accepted by `New[C]` (`auth/authjwt/parser.go:28`), which has no mapper — so `New[MyClaims](k, authjwt.Reject(fn))` would compile and do nothing, which is the "an option whose meaning depends on where it was passed" failure `Audience`'s own comment argues against (`parser.go:44-49`). Put it on the contract instead: `func Reject(a Authenticator, fn func(context.Context, Principal) error) Authenticator` in package `auth`, about eight lines, standard library only. The call site becomes one extra line on the short path, the error travels unchanged so guarantee 3 still holds, and it works for `apikey` and for a `Chain` of both — a dead session is not a JWT concern, and an API key has sessions too. **It is sufficient for this case, and the reason is worth stating**: the hook is handed a `Principal`, whose only claim accessor is `Attr(name)`, and on the `Standard` path `Attrs` is the whole payload (`auth/authjwt/claims.go:152`), so `p.Attr("sid")` and `p.Attr("jti")` reach it. On the `New[C]` + mapper path it works only if the mapper copied the id into `Attrs`, which is one sentence in the helper's doc comment and not a design problem.

### H-AUTH-14 — Read the caller in my own code
**Who:** every author, in the first service-layer function they write after mounting the middleware
**Wants:** the subject for an audit row, and the tenant as the `int64` it is.
**Story:** In a handler or a service method, they take the principal off the context and use two things from it.
**Must hold:**
1. Getting the principal is one call, and there is no answer without one on a route that required authentication.
2. A claim comes back as the Go type it was written as, without the call site doing the work twice.
3. "The claim is not there" and "the claim is there and is not what I asked for" stay two different answers.

**Today:** 🟡 partial
**Evidence:** 1: `auth.Require` (`auth/context.go:45`), failing closed, pinned by `TestAnAbsentPrincipalFailsClosed` (`auth/context_test.go:24`) — and `crud/decorators/security/principal.go:169-186` is why failing closed matters. 2: `Attr` returns `(any, bool)` (`auth/principal.go:39`) and `auth` declares no generic function at all (`docs/api/surface.md`, the `auth` section), so every read is a lookup, an ok-check, a type assertion and a second ok-check. 3 holds today only because the hand-written four lines happen to keep the two apart.
**If not ready:** four lines per claim, every time, or a helper in the consumer's own package. The security decorator absorbs it for the rules that matter (`crud/decorators/security/principal.go:143`), so it bites only in application code — which is most of the code. `auth.Attr[T](p, name)` is a few lines in the library and removes four from every call site, **and its specification is the whole of it**, in two parts.

The conversion arm: `narrow` yields exactly `int64` for an integral number, `float64` otherwise, plus `string`, `bool`, `map[string]any` and `[]any` (`auth/authjwt/claims.go:67-95`), while a hand-built principal (H-AUTH-04) commonly holds an `int`. So `auth.Attr[int](p, "tenant")` — the spelling everybody types first — must not silently miss: the helper carries a numeric conversion across integer widths and refuses only when the value does not fit.

The return shape: `(T, bool)` collapses "absent" into "wrong type", and that is the distinction `Principal.Attr`'s second result exists to draw (`auth/principal.go:36-39`) and the one `attrOf` refuses to lose. Make it `auth.Attr[T](p, name) (T, error)`, or ship both and say in the doc comment which one to reach for. Written with neither arm, the helper turns four visible lines into one invisible miss and a scope that narrows to nothing, and it is worse than the four lines.

### H-AUTH-15 — Run the whole thing with no identity provider: in a test, and in a cron job
**Who:** the author writing the first handler test, and the author writing the nightly job
**Wants:** a fabricated caller, without minting a token.
**Story:** In a unit test they build a principal and put it on the context before calling the repository. In the nightly export they do the same with a service identity, so the gated repository works from a `main` with no HTTP in it.
**Must hold:**
1. Building a principal from nothing is one composite literal, with no interface to implement.
2. Putting it where the repository will find it is one call, and it is the same call the middleware makes.
3. An end-to-end HTTP test can present a real credential without a JWT library in the test's imports.

**Today:** 🟡 partial — 1 and 2 are ready; 3 is reachable and demonstrated nowhere.
**Evidence:** `auth.Claims` (`auth/principal.go:49`) and `auth.WithPrincipal` (`auth/context.go:22`) — the same function every binding calls, which is what makes the fake faithful, and the reason the principal may live nowhere else is written into that comment and into [[D-055]]. For 3, `apikey.Static` swapped in for the JWT authenticator (`auth/apikey/apikey.go:126`) is the route, and nobody in this repository has taken it: `grep -rn apikey test/ _examples/` is empty. That is the same evidence gap this file scores as "proven nowhere" for H-AUTH-01 guarantee 4, and it should be graded the same way.
**If not ready:** for 3, one `_examples` or one integration arm mounting a guard over `apikey.Static` — which also closes half of blocker 3. Beyond that, one boundary belongs at the front of this document rather than in a footnote: **there is no signer here, by design** ([[UC-019]] Out of scope, `auth/doc.go:20-22`). A test that must exercise the *JWT* path mints its own token, and the repository's own example shows the pattern in twenty lines (`_examples/auth-jwt-gin/main.go:169`). Whether that stays true is a decision worth making rather than inheriting: every consumer who mounts this middleware and tests it writes the same `golang-jwt` fixture, and `authjwt.Standard`'s twin — a two-line `authjwttest.Sign` in a test-only package — would cost one file and no dependency the module does not already have. `docs/modules/en/authjwt.md:178-181` forecloses it without argument.

### H-AUTH-16 — The credential is not in the Authorization header
**Who:** the author of a browser app that keeps its session in an HttpOnly cookie, and the same author's event stream
**Wants:** the guard to read the cookie; and, for the stream that cannot set headers, the `access_token` query parameter.
**Story:** They look for the option that says where the credential is. They find `Header` and `Lookup`.
**Must hold:**
1. Where a credential is read from is one option on the guard, so all four transports get it at once — and an option that means nothing on one transport says so.

**Today:** 🟡 partial — **the cookie half is built**: `authhttp.Cookie(name)` (`auth/http/authhttp/cookie.go`), documented on the authhttp module page and proven by `cookie_test.go`. Query parameters are still out of reach.
**What settled the cookie half:** a consuming application moved its browser to both credentials in HttpOnly cookies ([[D-075]]), so no Authorization header reaches the guard at all — the six lines of `Lookup` stopped being a convenience and became the only way in. It went to `authhttp` for the reason this entry already gave, and it falls back to the Authorization header, because `auth.Lookup` replaces the lookup rather than adding to it and this application also serves a native client that sends one.
**Evidence:** The option set is `Header`, `Lookup`, `Optional` and nothing else (`auth/guard.go:46-77`). The guard is handed a header getter, so a `Lookup` reaches a cookie only by asking for the raw `Cookie` header and splitting it — which works, and which no module page mentions. A query string is not a header on any transport, so no getter can see it.
**Two documents put the cookie path in a binding and this case is a challenge to neither.** `auth/doc.go:14-18` says every path that could put a principal into a context — "a header, gRPC metadata, a session cookie" — belongs to a transport, and [[UC-019]]'s Out of scope names "session cookies, CSRF and rate limiting" as different subsystems. Both are about session *management*: minting one, refreshing one, defending one against CSRF. Reading a credential out of a place is what `Header` and `Lookup` already are, and neither of them is a transport. So the option's existence is not the contested part; **its home is**, and this sweep yields it. The authhttp sweep argues for `authhttp.Cookie(name) auth.Option` and the argument wins on the property this module measures: `authhttp` already imports `net/http`, so the parser is `http.ParseCookie` rather than six hand-rolled lines, the three HTTP bindings get it free, and gRPC — where `get("Cookie")` is `""` and a cookie means nothing — never links it. Put it in `auth` and package `auth` grows its first `net/http` import, which is the exact build property [[D-055]]'s forbid gives as its reason. **`auth.Cookie` is withdrawn from this sweep's proposals; `authhttp.Cookie` is the one to build.**
**If not ready:** the query parameter is the consumer's own binding. (The cookie no longer is.) **The query half is a decision and it is auth's**, because it is about `Guard.Authenticate`'s signature. The cheaper of the two shapes wins: a namespaced key the bindings answer — `get("query:access_token")`, with gRPC answering `""` — keeps the documented `guard.Authenticate(r.Context(), r.Header.Get)` call intact (`docs/modules/en/auth.md:155`), where widening the getter to a source with `Header`, `Cookie` and `Query` methods breaks it. The counter-argument deserves recording: `Guard.Authenticate` is exported, so a hand-written binding that answers the query parameter is about twelve lines, and `test/integration/auth_jwt_test.go:63-69` is a working instance of exactly that shape — which weakens the case for widening the getter at all. The per-binding half belongs to the authhttp sweep, which owns the four getters. What is decided here is whether the getter's shape changes, and it should be decided before the tag because only one of the two candidates is still free afterwards.

### H-AUTH-17 — Find out why the 401s started at 14:20
**Who:** whoever is on call
**Wants:** one line, on the server, saying which check failed.
**Story:** After a deploy, every request is a 401. The response body says `unauthenticated` and *authentication is required*, which is by design. They go to the logs.
**Must hold:**
1. The response body still names no reason.
2. The reason is reachable inside the process without reading this library's source.
3. A refusal the consumer's own code produced is still distinguishable, server-side, from one the library produced.

**Today:** 🟡 partial — 1 holds and is control-cased; 2 is possible and undocumented at the one place a consumer would look; 3 fails for anyone who follows the documented advice.
**Evidence:** 1 is the point of the module: `auth/errors.go:33`, pinned by `TestTheReasonForA401NeverReachesTheBody` (`auth/errors_test.go:82`), with the control that asserts the leak *is* there when the reason is put where it would obviously go. For 2, [[UC-019]] guarantee 6 promises the reason is *recoverable*, and it is: `docs/modules/en/auth.md` spells out `errors.As` down to `*errs.Fault` and then `Unwrap`, noting that `errors.Unwrap` does not reach it because a fault unwraps to a slice (`errs/fault.go:104`). **Recoverable holds and is pinned. Nothing reports it** — and the two are not the same guarantee. For 3: `auth.Unauthenticated` takes a `string` and wraps only `ErrUnauthenticated` (`auth/errors.go:33-38`), so a mapper refusing a revoked session interpolates its own sentinel into a message and `errors.Is` against it fails from that point on — the exact branch a denylist owner wants in their alerting.
**If not ready:** the recovery is four lines of `errors.As` and `Unwrap` that every consumer writes identically, against a shape `errs.Fault.Error()` deliberately does not carry (`errs/fault.go:68`, [[D-047]]). Auth's half is two functions, not one, and specifying only the first is the version that has to be revisited: `auth.Reason(err) string` for the log line, and `auth.Refuse(cause error) error` producing the identical flat 401 while keeping `cause` reachable by `errors.Is` and `errors.As` server-side. Both must be unreachable from a renderer — `auth/errors_test.go:82` is the control that would catch that mistake. **Where the line is written is the authhttp sweep's** (its blocker 4): `port.Logger(ctx)` on the refusal path, on the two transports that drop the error — **one incident, two halves, count it once.** Two notes for whoever builds it. It is a *new category* for [[D-062]], whose nine sites are all failures nobody can be returned an error for; a 401 is returned to the client and its volume is attacker-controlled, so the proposal needs a level and a field list — never the credential — or it ships and gets turned off in the first credential-stuffing run. And [[D-055]]'s "do not import `port` from `auth` or any `auth*` binding" is already broken by `auth/http/authhttp/authhttp.go:29`, with D-062 naming "the shared auth half" among its own call sites: the two decisions disagree in writing, D-062 is the later one and the tree follows it, and **D-055 needs the amendment before anyone cites it to block this**. Fiber does not import `port` today, so closing it there is either that import or a shared helper in `authhttp`.

### H-AUTH-18 — The twentieth resource
**Who:** the same author, six months in, with twenty gated repositories and role definitions split across feature packages
**Wants:** the permission vocabulary to still be one vocabulary.
**Story:** They add the twentieth resource, copy the policy from the nineteenth, and rename `note:` to `invoice:`. One verb of one resource now refuses everybody, forever. Separately, two feature packages both define the `editor` role, and merging the two maps keeps one package's permissions.
**Must hold:**
1. A permission named by a policy that no role grants is discoverable without production traffic.
2. Two role maps can be combined without one silently winning.

**Today:** ❌ missing
**Evidence:** The role map is a value the application wires (`auth/principal.go:102-105`, and `auth/doc.go:29-35` refuses a registry for good reasons that are not in question here). Twenty `security.PerAction` and `RequirePermission` policies name permission strings (`crud/decorators/security/principal.go:34`, `:103`); nothing links the two sets, nothing enumerates either, and a typo fails closed as a 403 on one verb of one resource with no reason on the wire and none in a log (H-AUTH-17). For 2, `RoleMap` is a bare `map[Role][]Permission` (`docs/api/surface.md`, the `auth` section), so combining two is `maps.Copy` or a hand loop and a role named in both keeps one side's permissions rather than the union — the same permanent-403-on-one-verb failure, arriving from the direction that scales with team size rather than resource count.
**If not ready:** they find it from a support ticket. Closing 1 is the same shape [[D-021]] already licenses for a parser that would over-trust — a check at wiring time that every permission a policy names is granted by some role in the map — and the two halves are in different subsystems: one exported enumerator here (`RoleMap` already has `Expand`; a `Permissions()` is three lines) and the check itself in `security`. **The check is the security sweep's blocker 6 — count it once.** Closing 2 is `RoleMap.Merge` in the same three lines, unioning rather than overwriting, and it is worth shipping with the enumerator because both are additive today and neither is obvious after a tag.

### H-AUTH-19 — Forward the caller's identity to the next service
**Who:** the author of the second service, on the day the first one has to call it
**Wants:** the credential that authenticated this request to reach the service this handler calls.
**Story:** A request arrives with a token. The handler reads an article through a `remote` resource, and the content service refuses it, because nothing put an `Authorization` header on the outbound call.
**Must hold:**
1. What authenticated this request is reachable from the outbound call site, without the consumer inventing a second context key.
2. Whatever carries it obeys the same rule the principal obeys: nothing but the context, and nothing renders it.

**Today:** ❌ missing
**Evidence:** `Guard.Authenticate` uses `cred` and drops it (`auth/guard.go:95-113`); only the principal reaches the context (`auth/context.go:22`). `grep -rn "WithCredential\|CredentialFrom" --include="*.go" .` is empty. The credential exists in the right place at the right moment — `authhttp` has already parsed one out of the header at the inbound edge — and is thrown away.
**If not ready:** the consumer defines their own context key, re-reads the `Authorization` header in a middleware of their own, and stashes it — which is precisely the "header their handler copies into a struct and threads down four call frames" this document's opening says they came here to avoid, and it is about twenty lines across two packages for the first resource. **This is not somebody else's item to raise.** The remote sweep already writes `auth.CredentialFrom(r.Context())` into its ideal call site for a symbol that does not exist (`docs/ai/usecases/modules/remote/Remote.md:723-726`) and says the decision has to be taken against [[D-055]] (`:257-269`). It is an auth decision because a second value travelling the way the principal travels is the same move D-055 governs, and **it is free only before the tag**: `auth.WithCredential` / `auth.CredentialFrom` plus an opt-in in `authhttp` that keeps the credential beside the principal is additive today, and a second context key added later is a second answer to "where does identity live". The forbid it has to be weighed against is real — D-055's invariant says `auth` holds "no credential store" — and a credential in flight is not a store, which is the argument, and it should be written into D-055 as an amendment either way. Say no if the answer is no; saying nothing leaves every two-service consumer inventing the key.

### H-AUTH-20 — The roles are not in the token; they are in my database
**Who:** the author whose tokens carry a user id and nothing else, because roles change without waiting for an expiry
**Wants:** to load roles and the tenant on each request, from their own tables, and have the rest work unchanged.
**Story:** They point a parser at their issuer, write a mapper that reads one row, and build a principal from it.
**Must hold:**
1. There is one supported place for that read, and it is on the path every request already takes.
2. What it costs is stated: a database round trip per request, on the hot path.
3. If that read fails, the caller gets a 500 and not a 401.
4. If they cache it, what the cache trades away is stated in the same place, the way the API-key story states it.

**Today:** 🟡 partial — 1 holds; 2, 3 and 4 are unwritten.
**Evidence:** The mapper is the place (`auth/authjwt/authenticator.go:22`), it takes a `context.Context` for exactly this, and its error travels unclassified unless built with `auth.Unauthenticated` — so 3 holds by construction and is pinned nowhere, the same gap as H-AUTH-13's. For 2: nothing in `docs/modules/en/authjwt.md` mentions that the mapper runs per request or that it commonly does I/O, and there is no caching seam anywhere in the subsystem. For 4: this file already treats the identical trade as a guarantee worth naming for API keys (H-AUTH-05 #6) and the JWT path, where far more consumers meet it, says nothing.
**Cliff, again:** `Standard` has no mapper argument, so these consumers are on the long form from hour one — a fifth arrival at H-AUTH-13's cliff, alongside a Keycloak token, a nested claim, two issuers and a denylist.
**If not ready:** they write the mapper and discover the latency in a flame graph. The fix is a paragraph, not code: say that the mapper runs on every request, that a database read there is the intended shape, that its error must be built with `auth.Unauthenticated` only when it is a refusal, and that caching it trades away exactly what caching an API key trades away — a role revoked at 14:00 keeps working until the entry expires. A `Reject`-shaped `auth.Enrich` is *not* the answer here; the mapper is already the seam and a second one would be two places to look.

### H-AUTH-21 — One process, REST for the browser and gRPC for the internal callers
**Who:** a platform team exposing the same service twice
**Wants:** one guard, one identity, one refusal, on both.
**Story:** They build the guard once in `main`, mount it as Gin middleware and as a gRPC interceptor, and expect a policy on the repository not to know which door the request came through.
**Must hold:**
1. The same guard value drives both, and neither needs a second one.
2. The refusal is each transport's own — 401 over HTTP, `UNAUTHENTICATED` over gRPC — with the same silence.
3. A decision changed on the guard changes on both at once.
4. A long-lived stream authenticated when it opened is a known boundary, not a surprise.

**Today:** 🟡 partial — 1, 2 and 3 hold and are pinned; 4 is decided in a line no consumer of this document will read.
**Evidence:** [[UC-019]] guarantee 15, and the mechanism is `Guard.Authenticate` taking a `func(name string) string`, which [[D-055]] names verbatim as the reason there are not four copies. The gRPC binding supplies it from case-folded metadata (`auth/rpc/authgrpc/interceptor.go:109-126`), the HTTP ones from `r.Header.Get`, `c.GetHeader` and a closure over `c.Get`. `TestADoubleInstallAuthenticatesOnce` and the optional-guard tests are carried file for file by the three HTTP bindings and in the gRPC vocabulary by the fourth, which is what makes 3 a test failure rather than a production discovery. For 4, [[UC-019]] Out of scope says a stream is authenticated when it opens and one that must re-check does so itself; `auth/rpc/authgrpc/interceptor.go:67` wraps the stream's context once.
**Worth stating because nothing else does:** this is also the only sweep that covers `auth/rpc/authgrpc` at all — there is no readiness file for it under `docs/ai/usecases/modules/`. What is *not* symmetric is worth the owner's eye before the tag: gRPC has `Skip(fullMethods...)` (`auth/rpc/authgrpc/interceptor.go:28`) and the three HTTP bindings have no route exemption, which is the authhttp sweep's blocker 5.
**If not ready:** for 4, one sentence in `docs/modules/en/authgrpc.md` — a stream is authenticated at open and a token that expires mid-stream does not close it — because the only place it is written is a use case a consumer does not read. Free, and it removes a class of "we thought this was covered".

### H-AUTH-22 — The issuer hands out a public key, not a key set
**Who:** the author of an internal service whose issuer publishes one PEM, in a config map or a secret
**Wants:** to verify against it, without a key-set endpoint and without learning a JWT library.
**Story:** They find `authjwt.RSA` in the table, read its signature, and discover it takes a `*rsa.PublicKey`.
**Must hold:**
1. The three static asymmetric sources are as reachable as `JWKS` — the PEM-to-key hop is shown, or shipped.
2. A key parsed the wrong way fails at start-up, not at the first request.

**Today:** 🟡 partial — declaration validation is complete and the PEM hop is missing.
**Evidence:** `RSA`, `ECDSA` and `EdDSA` each pin their methods, reject nil,
malformed and weak material at declaration, and deep-copy mutable coordinates,
moduli or bytes. `TestStaticAsymmetricKeysAreValidatedAtDeclaration` and
`TestStaticAsymmetricKeysAreSnapshottedAtDeclaration` pin both properties; JWKS
reuses RSA's complete 2048..16384-bit odd/composite/coprime modulus and
sane-exponent validation, and Ed25519's canonical non-low-order point
validation. The module page
lists the constructors, but no snippet anywhere shows how to get a
`*rsa.PublicKey` from a PEM: `grep -rn "ParsePKIX\|pem.Decode" --include="*.go"
.` is empty across the whole repository and `_examples/`.
**If not ready:** the consumer writes `pem.Decode` plus `x509.ParsePKIXPublicKey` plus a type assertion, and has to know not to reach for `ParseCertificate` when the file is a certificate rather than a bare key — a first-hour mistake whose symptom is the same reasonless 401 everything else here produces. Four lines in `docs/modules/en/authjwt.md` under the key-source table closes it and costs nothing. A shipped `authjwt.RSAFromPEM([]byte) KeySource` that panics on a PEM it cannot parse is the [[D-021]]-shaped version and is additive; it is worth deciding now only because after a tag the two spellings both exist forever.

### H-AUTH-23 — Turn authentication on for a service that already has traffic
**Who:** the team retrofitting auth onto a fleet of clients they do not all control
**Wants:** to find out how many live requests would be refused, and why, before refusing any of them.
**Story:** They mount the guard in a measuring mode, watch for a week, chase the clients that show up, and then flip it.
**Must hold:**
1. There is a mode in which a request that would be refused is counted rather than refused.
2. What would have refused it is reachable, so "which clients" is answerable.

**Today:** ❌ missing
**Evidence:** `Optional()` (`auth/guard.go:76`) lets an *absent* credential through and still refuses a bad one (`auth/guard.go:96-106`) — so it is not a shadow mode; and an optional guard in front of a gated repository turns the anonymous requests into a 401 from the repository instead of from the door, which [[UC-019]]'s Status states plainly. There is no counter, no callback and no logger on the guard, and H-AUTH-17's reason is unreported even for the refusals that do happen.
**If not ready:** and the honest answer may be "we do not do this" — in which case the recipe is four lines nobody has written. Mount the guard optional, do **not** gate the repository yet, and log the principal-absent rate from the consumer's own handler with `auth.PrincipalFrom`; then gate. That is a section in `docs/usage-guides/`, costs nothing, and without it the first rollout is a change nobody can size in advance. A guard-level shadow mode is the larger version and this sweep does not ask for it: an option that authenticates, records, and lets the request through regardless is an option one letter away from disabling authentication in production, and that is the wrong thing to make easy.

### H-AUTH-24 — Build it once, serve every request through it
**Who:** every author, implicitly, in `main`
**Wants:** to construct the guard, the parser and the key source at start-up and share them across every request.
**Story:** They build one guard in `main` and hand it to a middleware. They never think about it again.
**Must hold:**
1. A guard, a parser, an API-key authenticator and a JWKS key source are safe to use from many goroutines at once.
2. Nothing per-request is stored on any of them.

**Today:** ✅ ready — and stated nowhere.
**Evidence:** `Guard`'s fields are set by options at construction and only read
afterwards. `Parser[C]` holds the key source and a fixed option slice, while
`keyfuncFor` closes a request context over one call rather than storing it. The
JWKS source locks its key map, explicit fetched/attempted states and in-flight
record. The fetch runs as detached bounded singleflight work; every initiator
and waiter selects independently on its context. `TestAConcurrentBurstOfMissesIsOneFetch`,
`TestAConcurrentFailedRefreshSharesOneErrorAndOneFetch`,
`TestALeaderThatDisconnectsDoesNotFailTheWaiters` and
`TestAWaiterCanCancelWithoutStoppingTheSharedFetch` pin the shared-state and
lifetime branches under `-race`. `apikey`'s authenticator is a construction
snapshot read after `New`.
**If not ready:** n/a. The gap is documentation of a property that already holds: no module page says any of this, and the alternative a cautious consumer reaches for — a mutex around the guard, or a parser built per request — costs a JWKS fetch per request and is not obviously wrong from outside. One line in each of the three module pages.

## The DX this should have

### The call site

```go
// The auth half of day one: 8 code lines, 9 library names, 4 packages.
roles := auth.RoleMap{"editor": {"article:read", "article:write"}}

guard := auth.NewGuard(authjwt.Standard(
	authjwt.JWKS("https://id.example.com/.well-known/jwks.json"), roles,
	authjwt.Issuer("https://id.example.com"),
	authjwt.Audience("articles-api"),
	authjwt.Leeway(30*time.Second),
))

r.Use(crudgin.Errors(), authgin.Middleware(guard))
```

`RoleMap`, `NewGuard`, `Standard`, `JWKS`, `Issuer`, `Audience`, `Leeway`,
`Errors`, `Middleware` — across `auth`, `authjwt`, `crudgin` and `authgin`. This
compiles today, and nothing in the repository runs it: every runnable wiring here
uses `authjwt.HMAC` (H-AUTH-01).

Count it honestly: those eight lines authenticate and nothing more. Getting the
tenant into the `WHERE` clause — which is why anybody wires this — costs about
fourteen more lines and four more names, all of them `crud/decorators/security`'s:
`Combine`, `PerAction`, `ScopeAttr` and `Gate` on the `Bind`. The example's real
total is `_examples/auth-jwt-gin/main.go:85-108` plus `:138-148`, about
twenty-one lines. Those fourteen belong to the security sweep; quoting eight as
the total is the flattery this document exists to avoid.

### Turning one knob

Three knobs at once, because that is what month two looks like: their own claims,
a session denylist, and the token in a cookie because the app is a browser.

**Today** — 22 code lines, 13 library names:

```go
parser := authjwt.New[MyClaims](
	authjwt.JWKS(jwksURL),
	authjwt.Issuer(issuer),
	authjwt.Audience(audience),
)

authn := authjwt.Authenticator(parser, func(ctx context.Context, c MyClaims) (auth.Principal, error) {
	if err := revoked.Check(ctx, c.SessionID); err != nil {
		return nil, err // and it must already be an auth.Unauthenticated, or this is a 500
	}
	return auth.Claims{
		Sub:   c.Subject,
		Roles: rolesOf(c.Groups),          // expand once, here — never at check time
		Attrs: map[string]any{"tenant": c.Tenant},
	}.Grant(roles), nil
})

guard := auth.NewGuard(authn, auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
	for _, part := range strings.Split(get("Cookie"), ";") {
		if name, val, ok := strings.Cut(strings.TrimSpace(part), "="); ok && name == "session" {
			return auth.Credential{Scheme: auth.SchemeBearer, Token: val}, true
		}
	}
	return auth.Credential{}, false
}))
```

**The ideal** — 9 code lines, 11 names, and the two knobs that are not the
consumer's business have gone:

```go
parser := authjwt.New[MyClaims](authjwt.JWKS(jwksURL), authjwt.Issuer(issuer), authjwt.Audience(audience))

authn := authjwt.Authenticator(parser, func(ctx context.Context, c MyClaims) (auth.Principal, error) {
	return auth.Claims{
		Sub:   c.Subject,
		Roles: rolesOf(c.Groups),
		Attrs: map[string]any{"tenant": c.Tenant},
	}.Grant(roles), nil
})

guard := auth.NewGuard(auth.Reject(authn, revoked.Check), authhttp.Cookie("session"))
```

`Roles` is set before `Grant` on purpose: `Grant` returns its receiver unchanged
when the receiver holds none (`auth/principal.go:93`), so the tempting shorter
literal builds a principal with zero permissions and every rule refuses. That
footgun was in this document's own sample last round.

Two names in that block do not exist — `auth.Reject` and `authhttp.Cookie`. The
denylist is the expensive one: without `Reject` it lives inside the mapper, so a
consumer on `Standard` must rewrite the whole authenticator to add it
(H-AUTH-13).

Six more one-word reaches the ideal wants and the code does not have:

```go
tenant, err := auth.Attr[int64](p, "tenant")        // H-AUTH-14
cred, ok := auth.CredentialFrom(ctx)                // H-AUTH-19
authjwt.HMACAny(newSecret, oldSecret)               // H-AUTH-09
authjwt.AnyIssuerOf(oldIssuer, newIssuer)           // H-AUTH-08 — not a variadic Issuer
authjwt.JWKSStaleAfter(15 * time.Minute)            // H-AUTH-07
parser.Warm(ctx)                                    // H-AUTH-11
```

### Why this shape

The split that makes this module good is the one between the parser and the
bridge. `authjwt.New` produces something that knows nothing about `Principal`,
and `authjwt.Authenticator` is the second call — so a consumer who wants a JWT
parser and none of the rest stops after the first, and a consumer who wants a
denylist replaces the *mapper* and keeps the parser. That is what reaching
further without starting over looks like, and it is already here.

The cost of the alternative is visible in what `Standard` gives up. Collapsing
both calls into one is right for the ordinary token and wrong the moment
anything is unusual, because the collapsed form has nowhere to put the unusual
thing — and "unusual" turns out to include a Keycloak token (H-AUTH-02), a
nested tenant (H-AUTH-03), two issuers (H-AUTH-08), a denylist (H-AUTH-13) and
roles that live in the consumer's own database (H-AUTH-20). Five arrivals at one
cliff. The fix is not to remove `Standard`; it is that every hook a real
deployment needs should hang off the contract rather than off the JWT
constructor, which is why `auth.Reject` beats an `authjwt` option and works for
API keys too.

The guard's `func(name string) string` is the other load-bearing shape. It is
the reason four transports share every decision above it, and the reason a bug
fixed in the guard is fixed everywhere (H-AUTH-21). What it costs is H-AUTH-16 —
a request has more than one place a credential can be, and this getter sees one
of them.

### What it must not break

- **[[D-056]]** — the refusal names no reason to the client. `auth.Reason(err)`
  and `auth.Refuse(cause)` are server-side and must never be reachable from a
  renderer; the control case at `auth/errors_test.go:82` is what would catch the
  mistake. `WWW-Authenticate` is a D-056 forbid and this sweep does not
  challenge it — the case belongs to the authhttp sweep, which endorses it.
- **[[D-055]]** — `auth` holds no transport type, and the reason given is a
  build property, not a type-checking one: an `http.Header` in `auth` would put
  the gRPC interceptor out of reach. That is why the cookie helper goes to
  `authhttp` (H-AUTH-16) and why any query-parameter reach must stay
  string-shaped. D-055 also **writes down the getter itself** — its "Because the
  transport-neutral half is `auth.Guard`" paragraph names
  `func(name string) string` verbatim and cites [[D-045]] for it — so widening it
  is an amendment to D-055 rather than an undecided invariant, and D-045 (which
  does not mention `auth` at all) is not where that conversation goes. Two of
  D-055's forbids need attention rather than obedience: "do not import `port`
  from `auth` or any `auth*` binding" is already false
  (`auth/http/authhttp/authhttp.go:29`) and [[D-062]] overrode it without D-055
  being marked, and "do not resolve a role to permissions at check time" is
  unenforceable for a consumer-implemented `Principal` (H-AUTH-04). **And
  H-AUTH-19 is a challenge to it**: D-055's invariant says `auth` holds "no
  credential store", and `auth.WithCredential` puts a credential in flight into
  the context. This sweep argues in flight is not a store; the owner should say
  so in D-055 or say no.
- **[[UC-019]] Out of scope** — "session cookies" and "revoking a token before it
  expires". Neither is challenged. `authhttp.Cookie` reads a credential out of a
  place, which is what `Header` already does, and `auth.Reject` is the step in
  guarantee 10 moved one call outward so that `apikey` gets it too — if it
  lands, that Out of scope line has to name `Reject` alongside the mapper in the
  same change.
- **[[D-021]]** — construction panics on the misconfigurations that over-trust,
  and that stays. [[D-078]] deliberately extends it: non-positive
  `JWKSStaleAfter` is refused, the unset value takes a finite safe default, and
  that default remains longer than `JWKSMinRefresh`. `HMACAny()` with
  no secrets *must* panic, because `KeySource.valid()` would let it through
  (H-AUTH-09). H-AUTH-11 asks D-021 to be applied to the mirror case — a
  configuration that refuses everybody — which is an extension of the decision,
  not a challenge to it.
- **[[D-062]]** — logging a refusal reason goes through `port.Logger(ctx)`, and
  it is a new category for that decision rather than one of its nine sites. A
  `log.Printf` in `authhttp`, or an `OnRefusal` callback option, would both be
  defects.
- **[[D-036]]** — the published root module may require another module of this
  repository and nothing else, and `make check-deps` is what holds it. Nothing
  proposed here adds a requirement anywhere: `auth.Reject`, `auth.Attr`,
  `auth.Refuse` and `auth.WithCredential` use the standard library or less, and
  `authhttp.Cookie` uses `net/http`, which `authhttp` already imports. (D-036
  amends [[D-033]]; D-033 is the wrong citation for this and was the one this
  file used last round.)

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| Day-one wiring for a hosted provider | Eight lines, and no runnable proof anywhere that they work against a key set | none in code · large in evidence |
| An ordinary token from an ordinary provider | Auth0's namespaced claims need a mapper; Keycloak and Cognito spell roles elsewhere and yield the OAuth `scope` string as their permission vocabulary; Entra's `roles` works and its `scp` is unread | small — a mapper, on day one |
| Your own claims struct, no library types in it | `New[C]` + `Authenticator`, two calls, as designed | none |
| A tenant claim the issuer nested | A four-line `security.ScopeField` closure that is in no document and has an unwritten fail-closed obligation | small in lines · large in what it risks |
| An identity the service already has | Implement four methods — and expand roles yourself, correctly, unaided | small · unenforceable |
| Refusals indistinguishable to a client | Free, and control-cased | none |
| One credential kind or several at one route | `Chain` + `Lookup`, ~6 lines, and outages survive the wiring order | none |
| An API key in the header the fleet already sends | `apikey.Header("X-Api-Key")`; the module pages use it | none |
| A machine caller in a tenanted service | Both documented examples build a principal with no `Attrs`, which the flagship policy then denies on every request | small — one line of docs |
| A fabricated caller for tests and jobs | One literal, one call | none |
| A real credential in an end-to-end test | One package test walks Guard → API-key authenticator → static store, with an Authorization control | none in code · small in runnable examples |
| A token for a test of the JWT path | `golang-jwt` in your test module, ~20 lines | small, and foreclosed by documentation |
| A hook that refuses a verified token | Only off `Standard`'s path: 5 lines and 5 names become 11 and 11 | small |
| Roles loaded from my own database | The mapper, which is right — and nothing says it runs per request or what caching it costs | none in code · small in docs |
| A typed claim at the call site | 4 lines per claim, forever | small |
| Two secrets during a rollover | `Custom` plus `golang-jwt` in your imports, or a `Chain` | small |
| Two issuers during a migration | A `Chain`, and nothing anywhere warns about colliding subjects | small · sharp |
| Two audiences during a rename | A `Chain`; `Audience("a","b")` means both-of, and the one-word reach stops checking entirely | small · sharp |
| A static public key from a PEM | `pem.Decode` + `x509.ParsePKIXPublicKey` yourself, shown nowhere in the tree | small |
| Clock skew tolerated | One line that exists, is well documented, and is in no canonical snippet | none in code · small in docs |
| A stricter guard on a subtree | Mount ordinary A then stricter B once each: B always runs and its principal reaches the handler. Only adjacent A -> A is idempotent; ambiguous A -> B -> A fails closed because guards declare no assurance order | none for A -> B · small composition constraint for re-entry |
| A credential in a cookie | `authhttp.Cookie(name)`, documented on the authhttp page | none |
| A credential in a query string | No reach from any binding | large |
| Forwarding the identity to the next service | Your own context key and your own middleware, ~20 lines, in the one place this library said you would not need one | large |
| One guard on HTTP and gRPC at once | Works, is pinned file for file, and is the best thing here | none |
| Building it once and sharing it | Safe, and said nowhere | none in code · small in docs |
| A revoked JWKS key stops being accepted | Five-minute safe default, configurable; exact fake-clock boundary is pinned | none |
| A provider outage that looks like an outage | Typed unavailable; bounded stale-on-error is explicit and observable | none |
| Knowing a deploy's JWKS cannot work | `Parser.Warm(ctx)` before readiness; typed provider failure and one shared fetch | none |
| Turning auth on without an incident | No shadow mode, and no recipe for the one you can build | large |
| Knowing why a 401 happened | Recoverable in four lines nobody has written for you; reported nowhere | large |
| A permission vocabulary that stays one vocabulary | Nothing links policies to role maps, and two role maps merge by overwriting | large, and it grows |

**Overall:** the contracts are right, and they are the hard part — the reasonless
401, the algorithm pinned to the key, roles expanded once, the three-result key
store, an outage beating a refusal whatever the wiring order, one guard driving
four transports. Every one of those is decided deliberately and pinned by a test
that would fail if somebody undid it. What is not right is the rest of the first
hour and the second service. The dedicated API-key header now matches the
ordinary client, and a stricter nested guard runs without an opt-in ([[D-076]]);
`Standard` against a provider that spells roles anywhere but `roles` still produces a principal
full of OAuth scopes and a 403 on everything. Past the first hour the pattern is
consistent: `Standard` has no seam, so the first real requirement drops you to
the long form, and five of the cases above arrive at that same cliff from
different directions. Then the second service arrives and the identity this
library established cannot leave the process. Provider health now has its own
typed channel, withdrawn keys have a finite lifetime, and `Parser.Warm`
validates remote trust before readiness ([[D-078]]). Audience typos need
external configuration/discovery validation; clock drift and a kid-less token
against a two-key set retain their stated operational rules.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 4 | `authjwt.Claims` reads roles from `roles` only and takes the OAuth `scope` string as permissions verbatim (`auth/authjwt/claims.go:24`, `:142-146`), and nothing says so at the call site | serious | A Keycloak or Cognito token yields a principal holding `openid profile email` — permissions that look real, match no policy, and 403 every request with no reason anywhere, on day one. `scp` is a third permission spelling nothing reads. |
| 5 | No supported way to forward the caller's credential to the next service: `Guard.Authenticate` drops it and `auth.WithCredential`/`CredentialFrom` do not exist | serious | Every two-service consumer invents a second context key and a second middleware — the thing this library's opening promises they will not need. The remote sweep already writes `auth.CredentialFrom` into its ideal call site. A second answer to "where does identity live" is free only before the tag, and it needs a yes or no against [[D-055]]. |
| 8 | Two tests that read as pins and assert almost nothing: `TestAMapperMayRefuseATokenThatVerified` asserts only `err != nil`; the `Chain` refusal subtest asserts the sentinel and nothing about disclosure | serious | The former does not distinguish 401 from 500 and the latter does not inspect disclosure. The JWKS outage test now asserts both typed unavailable and the absence of `auth.ErrUnauthenticated` ([[D-078]]). |
| 9 | No exported way to recover a refusal's reason, and nothing reports it (`auth/errors.go:33`; `errs.Fault.Error()` is classification only) | serious | [[UC-019]] guarantee 6 promises recoverable and delivers it; nothing promises reported. Auth's half is `auth.Reason(err)` **and** `auth.Refuse(cause error)` — the string-only version breaks `errors.Is` for the consumer's own refusals. The log line is the authhttp sweep's blocker 4 — one incident, two halves, count it once. |
| 10 | [[UC-019]] and `docs/ai/usecases/Index.md:90` say "covered — every guarantee is pinned", but guarantee 11's only pin asserts `err != nil` (blocker 8) | serious | Guarantee 8 now has same-guard and different-guard parity tests ([[D-076]]). Guarantee 11 still needs a classification assertion, so the blanket status remains too broad. |
| 11 | A nested or namespaced claim has no convenience form and the hand-written extractor carries a fail-closed obligation written nowhere (`crud/decorators/security/principal.go:169-186`) | sharp edge | The route exists — a `security.ScopeField` closure — and is in no document, so the version a consumer writes reintroduces `WHERE tenant_id = 0`. `ScopeAttrPath` is the fix and it belongs in `security`, not on `Attr`. The request-time panic on a type mismatch is the security sweep's blocker 5 — count that half once. |
| 12 | `authjwt.HMAC` takes one secret, `Issuer` one issuer, and `Audience` means all-of | sharp edge | The only one-line answers to "two issuers this month" and "two audiences this month" are `AllowAnyIssuer()` and `AllowAnyAudience()`, which stop checking. `AnyIssuerOf` also has to widen the start-up check at `parser.go:111`; the `HMAC` half wants `HMACAny` rather than a variadic, because a variadic breaks the function value and a zero-argument call passes `valid()`. Nothing warns that two issuers can mint the same subject. |
| 13 | A credential in a query string is unreachable from any binding | sharp edge | Event streams cannot set headers. **The cookie half is closed**: `authhttp.Cookie` is built, falls back to the Authorization header, and is what a browser holding both credentials in cookies authenticates through ([[D-075]]). The query half is a decision and must be taken before the tag, because widening `Guard.Authenticate` breaks a documented call and a namespaced key does not. |
| 14 | `apikey` has no key generator and no caching seam, and its documented principal names no tenant | sharp edge | The mint half of the ten lines is undocumented while the hash half is; the first caching wrapper a consumer writes will trade revocation away without saying by how much; and a partner key copied from the page is denied by the flagship row filter. |
| 15 | Nothing links the permission strings a `RoleMap` grants to the ones twenty policies name, and two `RoleMap`s merge by overwriting | sharp edge | A typo, or one role defined in two feature packages, fails closed as a permanent 403 on one verb of one resource with no reason on the wire and none in a log. The enumerator and `Merge` are auth's and additive; **the check is `security`'s and is that sweep's blocker 6 — count it once.** |
| 16 | Three of the five `KeySource` constructors have no PEM hop and no runnable wiring: `grep -rn "ParsePKIX\|pem.Decode"` over the tree is empty | sharp edge | An internal issuer publishing one public key is an ordinary deployment, and the four lines it needs are shown nowhere; reaching for `ParseCertificate` instead produces the same reasonless 401 as everything else here. |
| 17 | [[FL-019]]'s line numbers have drifted through `parser.go`, `claims.go`, `credential.go` and `authhttp.go` | sharp edge | A flow doc's one job is to say where something is. Step 5 → `credential.go:55` (`:56`); step 7 → `credential.go:36` (`:37`); step 8 → `parser.go:137` (`:161`) and `parser.go:110` for the method pinning (`:127`); step 9 → `parser.go:171` (`:195`); step 10 → `claims.go:121` (`:137`); step 14 → `authhttp.go:66` (`:67`). |

**Timing.** The JWKS status, freshness, detached fetch and readiness changes
landed before the tag under [[D-078]]. Blocker 5 adds a second context key to
`auth`, and a later one
is a second answer to the same question. Blocker 13's query shape either
preserves or breaks the documented
`guard.Authenticate(r.Context(), r.Header.Get)` call. Blocker 12's `HMACAny` is
free only in the second-constructor spelling; the variadic is not. Everything
else is additive or a documentation fix — and blockers 3, 4, 10, 11, 16 and 17
are documentation fixes that cost nothing and close four of the five first-day
failures today.

## Contested

- **Reviewer:** the FL-019 line drift is a documentation-consistency item, not an
  auth release blocker, and belongs in a consistency pass. **Kept** (as #17, a
  sharp edge): CLAUDE.md makes doc drift a defect rather than untidiness, and a
  tag freezes the map the next reader trusts instead of re-deriving. The step-7
  target is corrected this round from `:38` to `:37` — the same class of error
  the entry is about.
- **Reviewer (one of three):** H-AUTH-06 duplicates the authhttp sweep's `Chain`
  case and inflates the count. **Kept**, and the other two reviewers agree it
  belongs here: the guarantee is `auth.Chain`'s — an outage beats a refusal
  whatever the wiring order — and it is pinned in `auth`. It is downgraded to 🟡
  this round because its first guarantee is decided by [[D-056]] and asserted by
  no test.
- **Reviewer:** H-AUTH-12's stricter-guard defect is the authhttp sweep's, which
  already carries it as its blocker 1. **Kept here as well, and marked "count
  once" in both places**: the story is the bindings' and the fix is on
  `auth.Guard`, so a sweep that omitted it would hand the owner a fix with no
  home. The timing disagreement the two sweeps had is resolved in the authhttp
  sweep's favour — pre-tag — and the opt-in fix this file proposed in round 1 is
  withdrawn.
- **Reviewer:** H-AUTH-03 is ❌ because a nested claim cannot be named by any
  policy. **Corrected, not kept.** `security.ScopeField` is exported and a
  four-line closure reaches it, so the case is 🟡: what is missing is the
  convenience form, the documentation and the fail-closed obligation. The
  request-time panic is the security sweep's and is now marked as counted there.
- **Reviewer:** the file's Keycloak/Cognito/Azure story misreads how permissions
  are built. **Corrected.** `authjwt.Claims.Grant` folds the OAuth `scope` string
  into permissions before `auth.Claims.Grant` runs, so those principals hold
  scopes rather than nothing — which is worse; and Entra's app-role claim is
  spelled `roles` and does work, while its `scp` is a third permission spelling
  nothing reads.
- **Reviewer:** `auth.Cookie` collides with the authhttp sweep's `authhttp.Cookie`
  and would put `net/http` into `auth`. **Yielded.** `auth.Cookie` is withdrawn;
  `authhttp.Cookie` is the proposal. The query half stays here because it is
  about `Guard.Authenticate`'s signature.
- **Reviewer (two of three):** H-AUTH-19's `WWW-Authenticate` case belongs to the
  authhttp sweep, which already carries it with the same reasoning. **Removed.**
  Every path in its evidence was in another module and its fix had no home in
  `auth`. The number H-AUTH-19 is reused this round for credential forwarding;
  nothing outside this file cited the old one.
- **Reviewer:** the D-045 bullet claims the getter's shape is written down
  nowhere. **Corrected.** [[D-055]] names `func(name string) string` verbatim and
  cites D-045 for it, so the amendment belongs to D-055 and the ceremony is
  smaller than this file claimed. The observation that D-045 itself never
  mentions `auth` stands and is no longer load-bearing.
- **Reviewer:** the DX arithmetic undercounts and books `auth.RoleMap` onto the
  security sweep's ledger. **Corrected** to 8 lines / 9 names / 4 packages, with
  `roles` declared in the snippet, and `RoleMap` counted here where it belongs.
  H-AUTH-13's cliff is restated as 5 lines / 5 names → 11 / 11, which is a worse
  cliff than the previous round claimed.

## Edge cases

### E-AUTH-01 — A custom authenticator returns a typed-nil principal
**Shape:** misuse
**Setup:** An application implements `auth.Principal` on `*caller` and its lookup-miss branch returns `(*caller)(nil), nil`.
**What the consumer does:** They mount that authenticator behind `auth.NewGuard`, expecting the miss to be a refusal rather than an authenticated request.
**What must happen:** A principal with no concrete value is rejected before it reaches the context or a policy; an extension bug must not become a request-time panic or an apparent identity.
**Today:** ✅ handled
**Evidence:** Guard, Chain, context placement/retrieval, API-key lookup and the
`HasAll`/`HasAny`/`InAny` quantifiers share the interface-aware predicate in
`internal/nilvalue`. A typed-nil Principal is a refusal or absence before any
method is called. `TestAnAuthenticatorThatAnswersNothingIsARefusal`,
`TestANilPrincipalIsNotStored`, the typed-nil quantifier control, and the
nil-like Chain-success arms all carry a concrete typed-nil pointer rather than a
literal nil ([[D-076]]).
**Blast radius:** crash

### E-AUTH-02 — A typed-nil authenticator or API-key store starts the process
**Shape:** misuse
**Setup:** A tired author passes a nil `*myAuthenticator` as `auth.Authenticator`, or a nil `*myStore` as `apikey.Store`, after a failed constructor branch.
**What the consumer does:** They rely on the documented nil guard to make the deployment fail where it is wired.
**What must happen:** Both declarations refuse loudly at construction, regardless of whether nil is carried directly or through an interface.
**Today:** ✅ handled
**Evidence:** `NewGuard` and `apikey.New` reject nil-like dynamic values before
retaining their extension interfaces (`auth/guard.go`,
`auth/apikey/apikey.go`). `TestANilAuthenticatorRefusesToStart` and
`TestANilStoreRefusesToStart` each carry literal nil, a typed-nil pointer and a
typed-nil function; valid constructors elsewhere are their controls. Guard
options are opaque build declarations applied to a private draft, then copied
into the published guard. `TestNewGuardDoesNotPublishTheOptionDraft` mutates a
retained internal draft concurrently with requests and proves it cannot rewrite
the live security policy. `Guard.Validate` is called by all four transport
constructors, so nil and `new(auth.Guard)` fail while the graph is assembled;
direct Authenticate returns `ErrGuardNotReady` instead of panicking on the first
request ([[D-076]]). `Lookup` remains the low-level source escape hatch.
**Blast radius:** crash

### E-AUTH-03 — Pointer: credential cardinality is a transport decision
**Owner:** [Authhttp.md](../authhttp/Authhttp.md) owns repeated HTTP/gRPC credential handling. The core guard accepts one string-shaped credential; the binding must decide whether more than one header/metadata value is an ambiguity and refuse it before calling `Guard.Authenticate`.

### E-AUTH-04 — `Scheme("")` silently means `AnyScheme()`
**Shape:** misuse · seam
**Setup:** A scheme string comes from configuration and is blank because an environment variable was omitted.
**What the consumer does:** They expect a bad scheme declaration to stop the process, retaining the default `ApiKey` restriction unless they explicitly chose the named waiver.
**What must happen:** An empty replacement scheme is rejected at construction; only `AnyScheme()` may waive the scheme check.
**Today:** ✅ handled
**Evidence:** `Scheme` rejects empty and whitespace-only names while the
authenticator is built; only `AnyScheme` writes the internal empty waiver
(`auth/apikey/apikey.go`). `TestTheSchemeIsCheckedUnlessItIsWaived` carries the
default refusal, the explicit waiver, a valid replacement and both invalid
declarations ([[D-076]]).
**Blast radius:** data leak

### E-AUTH-05 — A `Static` API key's claims change after start-up
**Shape:** concurrency · seam
**Setup:** A small service wires `apikey.Static` with an `auth.Claims` principal whose `Attrs` map is later reused or updated by its configuration reload path.
**What the consumer does:** They rely on the documented fixed-at-start-up store and mutate the input only after it has been handed to the authenticator.
**What must happen:** Later mutations cannot alter the subject, permissions, or attributes an issued key authenticates as; concurrent mutation must not race request authentication.
**Today:** ✅ handled for the built-in fixed identity
**Evidence:** `Static` freezes value and pointer forms of `auth.Claims`, copying
roles, permissions and supported attribute containers at declaration time,
then materialises another deep copy for every lookup. `TryStatic` returns
`ErrUnsupportedStaticAttribute` for state reflection cannot copy soundly;
declarative `Static` panics on the same configuration error. In particular it
does not shallow-copy `bytes.Buffer`, `big.Int`, or a custom struct with hidden
mutable fields. A request can mutate a supported value it received without
changing the store or another request. The deterministic mutation proof is
`TestStaticSnapshotsClaimsAndReturnsAPerRequestCopy`; the concurrent control is
`TestStaticClaimsAreIndependentAcrossConcurrentRequests`, and pointer Claims
retain their concrete type without sharing.
`TestTryStaticRejectsMutableStateItCannotCopySoundly` carries all three refused
types, while `TestTryStaticCopiesSupportedStructAttributesForEveryRequest` and
the cyclic-container control prove fresh supported values. A custom Principal
cannot be enumerated through the four-method interface and must itself be
immutable and concurrency-safe; the contract says so rather than claiming an
impossible generic copy ([[D-076]]).
**Blast radius:** silent wrong answer

### E-AUTH-06 — The HMAC secret is empty
**Shape:** degenerate declaration
**Setup:** A deployment reads its HMAC secret from an unset or empty environment variable.
**What the consumer does:** They expect the parser construction to refuse a secret with no entropy rather than start as an authenticator every attacker can reproduce.
**What must happen:** `authjwt.HMAC` rejects an empty secret at construction.
**Today:** ✅ handled
**Evidence:** `HMAC` is exact HS256 and rejects fewer than 32 bytes at declaration;
the explicit HS384 and HS512 constructors require 48 and 64. The 0/1/16/31/32
boundaries and all three algorithm minima are pinned by
`TestHMACRefusesShortSecretsAtDeclaration`; exact algorithm selection is pinned
by `TestEachHMACConstructorPinsOneAlgorithm` ([[D-078]]).
**Blast radius:** none

### E-AUTH-07 — `Audience("")` passes the start-up check but authenticates nobody
**Shape:** degenerate declaration
**Setup:** The expected audience is supplied by an empty environment variable.
**What the consumer does:** They expect the same eager configuration rejection as when `Audience` was omitted.
**What must happen:** An empty audience element is rejected at construction, with a message that identifies the bad setting.
**Today:** ❌ wrong or unhandled
**Evidence:** `Audience` appends empty strings without validation (`auth/authjwt/parser.go:44-52`), while `New` only tests `len(s.audience) == 0` (`auth/authjwt/parser.go:111-145`). It consequently configures `jwt.WithAllAudiences("")`; golang-jwt treats an empty `aud` claim as missing and requires one whenever an expected audience exists (`github.com/golang-jwt/jwt/v5@v5.3.1/validator.go:237-269`). `TestAParserThatWouldOverTrustRefusesToStart` covers omitted audience, not `Audience("")` (`auth/authjwt/parser_test.go:167-203`).
**Blast radius:** confusing error

### E-AUTH-08 — A failed PEM parse yields a nil static public key
**Shape:** degenerate declaration
**Setup:** An internal issuer's PEM cannot be parsed, and the application passes the resulting nil RSA/ECDSA key or empty Ed25519 key to the corresponding constructor.
**What the consumer does:** They expect the authentication configuration to fail where the key is wired, not only after traffic reaches it.
**What must happen:** Every static-key constructor validates usable key material before `New` can build a parser.
**Today:** ✅ handled
**Evidence:** `RSA`, `ECDSA`, and `EdDSA` validate at declaration and panic on
nil or malformed material. RSA enforces an odd composite modulus from 2048
through 16384 bits, coprime to a sane odd exponent; ECDSA normalises to a
supported curve and checks the point; Ed25519 strictly decodes a canonical
32-byte point and rejects the low-order subgroup. All three deep-copy
caller-owned mutable material. `TestStaticAsymmetricKeysAreValidatedAtDeclaration`
pins the invalid matrix, `TestLowOrderEd25519TrustWouldAcceptAUniversalJWTForgery`
pins the security consequence, and
`TestStaticAsymmetricKeysAreSnapshottedAtDeclaration` mutates every original key
after construction while the parser continues verifying the pre-mutation token
([[D-078]]).
**Blast radius:** none

### E-AUTH-09 — A signed JWT has permissions but no subject
**Shape:** boundary · seam
**Setup:** An issuer emits a correctly signed, unexpired token with roles or permissions but omits `sub`.
**What the consumer does:** They use `authjwt.Standard` and expect every authenticated principal to have the stable caller identifier the auth contract promises.
**What must happen:** The bridge refuses the token, or makes an explicit no-subject waiver the consumer must name; it must not authenticate an identity whose audit and ownership key is empty.
**Today:** ✅ handled
**Evidence:** `Standard` refuses missing, empty and whitespace-only `sub` before
granting a principal. `TestStandardRefusesATokenWithoutASubject` pins all three
and carries the control: `New[C]` plus an explicit `Authenticator` mapper may
derive a stable subject from another claim ([[D-078]]).
**Blast radius:** none

### E-AUTH-10 — A JWKS contains the same `kid` twice
**Shape:** adversarial input
**Setup:** A provider publishes two usable keys with one key identifier, whether through a broken rotation job or a compromised key-set response.
**What the consumer does:** They expect the ambiguous key set to be refused; the verifier must not choose a trust anchor by document order.
**What must happen:** Fetching detects duplicate usable `kid` values and rejects the set without replacing a known-good cache.
**Today:** ✅ handled
**Evidence:** Fetch validates every entry's non-empty, unique id before filtering
or rendering any key, and installs nothing on an ambiguous response.
`TestAnEmptyOrDuplicateKidRefusesTheWholeKeySet` carries both negative cases and
the one-key control ([[D-078]]).
**Blast radius:** none

### E-AUTH-11 — The JWKS refresh interval is zero or negative
**Shape:** degenerate declaration · adversarial input
**Setup:** A duration parsed from configuration is zero or negative, then an attacker sends sequential tokens with unknown `kid` values.
**What the consumer does:** They expect the advertised anti-refetch limit to remain safe, or the invalid duration to fail at construction.
**What must happen:** `JWKSMinRefreshEvery` rejects non-positive durations (or preserves a safe minimum); unknown kids must not turn into one outbound fetch per request.
**Today:** ✅ handled
**Evidence:** `JWKSMinRefreshEvery` panics on zero and negative values.
`UnsafeJWKSNoMinRefresh` is the explicit waiver; the control in
`TestANonPositiveMinRefreshRefusesToStart` proves it permits two fetches for two
sequential misses, so the negative arm cannot pass vacuously ([[D-078]]).
**Blast radius:** none

### E-AUTH-12 — A thousand static API keys are configured
**Shape:** scale
**Setup:** A service grows from a handful of machine callers to a thousand and keeps the convenient `apikey.Static` wiring.
**What the consumer does:** They expect either a stated supported scale or a clear refusal before each authentication becomes a full linear scan.
**What must happen:** The module gives the declaration a bounded, documented capacity or directs the consumer to `Store` before production latency becomes a surprise.
**Today:** 🟡 partial
**Evidence:** `Static` deliberately compares every entry (`auth/apikey/apikey.go:114-149`), while both its source comment and module page describe a handful of keys / tests and small services (`auth/apikey/apikey.go:120-122`; `docs/modules/en/apikey.md:18-21`, `docs/modules/en/apikey.md:84-94`). There is no maximum, warning, or size test; `TestAKnownKeyAuthenticatesAndAnUnknownOneDoesNot` uses one key (`auth/apikey/apikey_test.go:12-55`).
**Blast radius:** confusing error

### E-AUTH-13 — The first authenticator says success but supplies no principal
**Shape:** partial failure · seam
**Setup:** A custom member in `auth.Chain` returns `(nil, nil)` for a credential it could not resolve, while a later authenticator could authenticate that credential.
**What the consumer does:** They expect the chain to treat "no caller" as a refusal and continue, consistent with the guard's contract.
**What must happen:** A nil principal with no error is not a successful authentication and cannot prevent a later authenticator from running.
**Today:** ✅ handled
**Evidence:** `Chain` snapshots and normalises its variadic input at
construction, skipping literal and typed-nil authenticators. A member returning
`(nil-like Principal, nil)` records a refusal and lets the next alternative run;
if none supplies an identity the result is `ErrUnauthenticated`. The typed-nil
member/success arms in `TestChainAnswersTheFirstAuthenticatorThatSucceeds` pin
both branches, while `TestChainSnapshotsTheVariadicSlice` mutates the caller's
input concurrently and proves a published chain observes only its snapshot.
**Blast radius:** confusing error

### E-AUTH-14 — A one-character-short API key is presented
**Shape:** boundary · adversarial input
**Setup:** A caller submits a key that is a prefix of a real static key, or an empty key.
**What the consumer does:** They expect neither input to authenticate and no prefix comparison shortcut to leak into matching behavior.
**What must happen:** Both inputs are refused while a full known key still authenticates.
**Today:** ✅ handled
**Evidence:** The authenticator rejects an empty token before lookup (`auth/apikey/apikey.go:95-111`), and `Static` uses `subtle.ConstantTimeCompare` for every candidate (`auth/apikey/apikey.go:135-149`). `TestAKnownKeyAuthenticatesAndAnUnknownOneDoesNot` pins a known key, an unknown key, a prefix, and an empty key (`auth/apikey/apikey_test.go:22-55`).
**Blast radius:** none

### E-AUTH-15 — A signing key is withdrawn before every token expires
**Shape:** partial failure | seam
**Setup:** A provider removes compromised key `k1` from its JWKS while unexpired `k1` tokens remain in circulation. No client presents a new, unknown `kid` to force a refresh.
**What the consumer does:** They need an explicit policy: either a known key remains accepted until token expiry, or the cache has a stated maximum age after which the provider is consulted. Revocation cannot depend on unrelated traffic or a process restart.
**What must happen:** The public API documents and enforces one acceptance/revocation lifetime, including what happens when refresh fails. A removed key must not remain trusted indefinitely by accident.
**Today:** ✅ handled
**Evidence:** A hit at the five-minute default `JWKSFreshness` refreshes the
whole set; `JWKSStaleAfter` changes the bound and only the explicitly unsafe
waiver removes it. `TestARetiredCachedKidStopsVerifyingAtTheFreshnessBoundary`
proves with a fake clock that the old key works before the boundary, fails after
the provider removes it, and the replacement works ([[D-078]]).
**Blast radius:** none

## Edge verdict

HMAC and JWKS now close their security-shaped edges: HMAC has exact algorithms
and declaration-time strength floors; JWKS rejects ambiguous ids, retains its
unknown-kid request bound under outage, refreshes cached hits on a finite clock,
and keeps provider failure out of 401 ([[D-078]]). Guard/Chain/context/API-key
seams now share nil-like semantics, Guard configuration and Chain input are
construction snapshots, and fixed built-in API-key identities do not share
mutable Claims state ([[D-076]]). Static asymmetric keys now share declaration
validation and snapshot semantics; empty audiences remain the declaration edge
here. Repeated credentials are an Authhttp transport seam, not a competing core
verdict.

## Release blockers found here (edge)

None remain from the typed-nil/immutability follow-up. The typed-nil Principal
that previously appeared authenticated is now refused consistently, and its
regressions exercise Guard, Chain, context, API key and policy-quantifier
boundaries ([[D-076]]). Other not-ready cases above retain their own status.

## Edge DX constraints

The round-2 DX conclusion is accepted with these boundaries. HMAC and static
asymmetric material plus JWKS refresh intervals now have construction-time
policies; empty audience elements still do not. `auth.WithCredential` and
`auth.CredentialFrom` are
likewise proposals, not current APIs: [[D-055]] must first decide whether an
in-flight credential may live beside the principal, then define explicit opt-in
placement and lifetime for every transport and remote forwarding. Until that
amendment, a Remote hook must use a consumer-owned context value or not forward
the credential. `authhttp.Cookie` remains HTTP-only. Refusal logging remains on
the [[D-062]] `port.Logger(ctx)` boundary. `JWKSDegradedObserver` is not a
refusal logger: it is the mandatory typed signal paired with an application
decision to keep using stale trust material for a finite window ([[D-078]]).

## Contested

- **H-AUTH-19's happy-half evidence misattributes credential parsing to
  `authhttp`.** `Guard.Authenticate` obtains the credential through
  `g.credential(get)` before authenticating it (`auth/guard.go:90-113`); the HTTP
  and gRPC bindings supply only their transport getter
  (`auth/http/authnet/authnet.go:49-55`, `auth/http/authgin/authgin.go:46-52`,
  `auth/http/authfiber/authfiber.go:48-56`,
  `auth/rpc/authgrpc/interceptor.go:48-58,67-77`). If [[D-055]] accepts
  credential retention, its opt-in and lifetime therefore belong at the
  Guard/transport-context seam, not at an imaginary Authhttp parser seam.
  `authhttp.Cookie` remains only a proposed HTTP credential *source*. The stale
  Remote pointer is replaced by its current explicitly conditional forwarding
  example (`docs/ai/usecases/modules/remote/Remote.md:1110-1117,1147-1150`);
  `auth.CredentialFrom` is not a current symbol.
