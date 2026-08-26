# auth · authjwt · apikey — establish who is calling, once, so every rule further in can ask

**Covers:** `github.com/frostgrove/vv/auth`, `github.com/frostgrove/vv/auth/authjwt`, `github.com/frostgrove/vv/auth/apikey`
**Sweep:** happy paths · release readiness
**Verdict:** not ready — the contracts are right and the refusals are right, but the documented first-day wiring for one of the two shipped authenticators refuses every client, a security-shaped default lets a stricter guard do nothing, a provider outage renders to every cold process as "your credentials are bad", and the second service a consumer adds cannot forward the identity the first one established.

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

**Today:** 🟡 partial — 1 and 2 are the best-decided thing in this subsystem; 3 fails on first deploy and the documented escape hatch does not rescue it; 4 is half done; 5 and 6 have nothing.
**Evidence:** The three-result `Store` is the seam and it is right: `auth/apikey/apikey.go:40`, the outage pass-through at `:104-107`, pinned by `TestAStoreFailureIsNotARefusal` (`auth/apikey/apikey_test.go:83`). Revocation needs no cache to bust because there is no cache: `Lookup` is the only path to a principal (`:103`).

For 3: `auth.Header` changes only *which* header is read; the value still goes through `ParseAuthorization`, which refuses a header with no space (`auth/credential.go:56-61`, called from `auth/guard.go:123`). So `X-Api-Key: k-batch-1` is "no credential presented" (`auth/guard.go:100`) and only `X-Api-Key: ApiKey k-batch-1` works. **`AnyScheme()` does not rescue it either** — `Guard.credential` runs before any authenticator is consulted, so `auth/apikey/apikey.go:97`, the check `AnyScheme` disarms, is never reached. Both module pages show the failing wiring (`docs/modules/en/apikey.md:33`, `docs/modules/en/auth.md:151`); `auth.md` shows the working `Lookup` nineteen lines later (`:170`) without saying that the option above it does not work, and `apikey.md` states the `ApiKey <token>` requirement sixty lines later under a heading a consumer copying a snippet never reaches (`:96-99`). The only test of `Header` sends `X-Auth: Bearer t` (`auth/guard_test.go:89`), so the bare value is untested — and the three `apikey` scheme subtests build an `auth.Credential` by hand (`auth/apikey/apikey_test.go:57-79`) and never touch a `Guard`, which is why the interaction is invisible to both packages' tests.

**And `apikey` has never been run end to end by this repository:** `grep -rn apikey test/ _examples/` is empty. No integration test, no runnable example. One of the two shipped `Authenticator`s exists only in its own package test.

For 4, the hashing half is documented well, in a code block, with the constant-time requirement attached (`docs/modules/en/apikey.md:64-82`). The generation half — `crypto/rand` plus an encoding — is nowhere. For 5, both shipped examples of a hand-built machine principal set `Sub` and `Permissions` and no `Attrs` (`docs/modules/en/apikey.md:29-33`, `docs/modules/en/auth.md:54-59`), and the flagship policy is `ScopeAttr("TenantID", "tenant")` — so a partner key copied from the page authenticates and is then denied on every request by `attrOf` ("the caller carries no tenant claim", `crud/decorators/security/principal.go:183`), with the reason dropped on the wire. Nothing found for 6.

**Obligation rather than guarantee:** constant-time comparison. `apikey.Static` compares every entry with `crypto/subtle` and explains why (`auth/apikey/apikey.go:114-125`, `:140`), but the case's own story is a database, where the timing property belongs entirely to the consumer's SQL. The package states the requirement — index by the hash of the presented key, because the hash is what travels (`auth/apikey/apikey.go:20-22`) — and cannot enforce it for a `Store` somebody else writes. It is documented as a requirement and should stay described that way, not counted as something the library keeps.

**If not ready:** for 3 they find it in a debugger and replace the option with a four-line `auth.Lookup` — the shape `auth/guard_test.go:106` already uses. The documentation fix is free today and it is two edits, not one: put `ApiKey <token>` into the wiring snippet at `apikey.md:33`, and change the scheme example `Scheme("X-Key")` (`apikey.md:103`) to a scheme-shaped name, because a header-looking name one section after `auth.Header("X-Api-Key")` reads as confirmation that schemes and headers are the same thing. The code fix belongs in `apikey`, not in `auth`: `apikey.Header(name string) auth.Option` returns the `Lookup` that synthesises `Credential{Scheme: DefaultScheme, Token: get(name)}`, so there is one string to get right instead of two that must agree across packages. For 4, five lines of `crypto/rand` and base64 appended to the section that already shows the hash. For 5, one line in the `Static` example giving the partner an `Attrs` map. For 6 they write a caching `Store` wrapper, and that is the sharp edge: **any cache of hits trades guarantee 2 away, and the trade is the whole design.** A cached hit is served without consulting the store, so a deleted row keeps working until the entry expires — caching *misses* cannot do that, it only delays recognising a key that was just issued. So `apikey.Cache(store, ttl)` has to state the number it costs ("revocation now takes up to `ttl`") and offer the invalidation call for the Tuesday afternoon, and it must key on the hash and never on the presented key, or the wrapper is a plaintext key store in process memory and undoes the discipline `Store` exists to enforce.

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

**Today:** 🟡 partial — 1 and 2 are done carefully; 3 and 4 are not done.
**Evidence:** 1: `auth/authjwt/jwks.go:128-141`, pinned by `TestARotatedKidIsPickedUp` (`auth/authjwt/jwks_test.go:84`). 2: the rate limit reads the last *attempt* rather than the last success, and a concurrent burst shares one in-flight fetch — `auth/authjwt/jwks.go:181-201`, pinned by `TestUnknownKidsDoNotBecomeOneFetchEach` (`:109`), `TestAFailingProviderIsStillOnlyFetchedOnce` (`:180`) and `TestAConcurrentBurstOfMissesIsOneFetch` (`:215`), with `TestALeaderThatDisconnectsDoesNotFailTheWaiters` (`:270`) for the case that made the fetch carry its own context. 3: `s.fetched` is written at `auth/authjwt/jwks.go:222` and read nowhere in the package, so a cached set has no maximum age and is only ever replaced when a token names a kid it does not hold. 4: a fetch failure returns from the keyfunc, is folded into the parse error, and leaves `auth/authjwt/parser.go:169` as `auth.Unauthenticated` — a 401. `TestAKeySetThatCannotBeReachedIsARefusalAndNotAPanic` (`auth/authjwt/jwks_test.go:148`) reads like the test that pins this and asserts only `err != nil`, so the classification is free to change without breaking anything.
**One shape that is decided rather than broken:** an issuer that mints tokens with no `kid` works only against a key set holding exactly one key (`auth/authjwt/jwks.go:143-145`), because anything else would be this package choosing which key to trust. That is right, it is documented (`docs/modules/en/authjwt.md:129`), and it is worth knowing that the failure it produces is the same reasonless 401 as everything else here — so a deployment that meets it after a provider adds a second key has no signal at all. It belongs in the same paragraph as `JWKSMaxAge`'s.
**How far 4 actually reaches, because it decides the ranking:** a warm process answers a known kid from the map before any network call (`auth/authjwt/jwks.go:131-133`), so an ordinary provider outage is invisible to callers holding tokens signed by a key the cache already holds. The 401 storm needs a cold cache: a deploy or a restart during the outage, an autoscaler adding a replica, or a rotation that introduces a kid nobody holds. Every deployment has one of those during a twenty-minute outage. Note the interaction with 3 — today's missing maximum age is exactly what makes a warm process survive, so the two fixes pull against each other and must be designed together.
**If not ready:** for 3, the practical rescue is that ordinary traffic on the *new* key triggers a refetch that replaces the whole map, so a rotate-then-revoke is usually repaired within seconds by the first legitimate token; a withdrawal with no new key introduced is not repaired until the process restarts. An option reading the field that is already there closes it, and the field is already correctly separated from `attempted` (`auth/authjwt/jwks.go:117-121`), which makes it a two-line change rather than a redesign. **Spell it `JWKSStaleAfter(d)`, not `JWKSMaxAge`:** the consts in this package are bare nouns (`JWKSMinRefresh`, `JWKSFetchTimeout`, `JWKSMaxBody`) and the options carry a verb, so `JWKSMaxAge` would read as a const and sit in autocomplete beside `JWKSMinRefreshEvery` as a second spelling of the same axis. Its composition has to be one sentence in the doc comment: a set older than the stale-after is refetched on the next token even when the kid is known, and the min-refresh limit still bounds the request rate. It must also say what it does when the age lapses and the provider is unreachable — serving stale keys through a failed refresh is what keeps 4 from getting worse — and an unset value must keep today's behaviour, or the tag changes what every existing deployment does. For 4 the parser has to tell a key-source failure from a verification failure and pass the former through unclassified, the way `apikey.Store`'s third result already does one package away.

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
**Story:** They ship a JWKS URL with a doubled letter. The process starts, the readiness probe passes, and every request is 401 forever.
**Must hold:**
1. A configuration that cannot possibly authenticate anybody does not start.
2. There is some point before the first request at which a consumer can find out.

**Today:** ❌ missing
**Evidence:** The three misconfigurations that make a parser *over-trust* do fail at construction, loudly and by name (`auth/authjwt/parser.go:101-116`, pinned by `TestAParserThatWouldOverTrustRefusesToStart` at `parser_test.go:167`), and `JWKS` panics on an empty URL for exactly this reason and says so (`auth/authjwt/jwks.go:41-47`). The mirror case has nothing: a URL with a typo, a blocked egress rule, a wrong issuer string or a wrong audience is discovered at the first request, as the same reasonless 401 a forged token gets, at every replica, forever. There is no `Warm`, no health seam, and the first request after each deploy parks behind a fetch bounded at ten seconds with concurrent waiters behind it (`auth/authjwt/jwks.go:80`, `:183-192`).
**If not ready:** nothing a consumer can write reaches inside the key source, so they discover it from the 401 rate. The closing shape is **`Parser.Warm(ctx) error`, called before `ListenAndServe`, and not a `JWKS(url, Warm())` option.** `JWKS` returns a bare `KeySource` with no error result, so a `Warm()` inside it has no caller-supplied context, no caller-chosen timeout and exactly one failure channel — panic — which turns a twenty-minute provider outage into a fleet that panics on start-up during a rolling deploy, blocker 2 moved from the request path to the boot path. Returning an error is what lets the caller decide, and the method must distinguish "the URL is wrong, or the set carries no usable key" from "the provider did not answer": the first fails the deploy, the second logs and starts. Both spellings need a `KeySource` field on `Parser` either way, because `KeySource` holds an opaque keyfunc and no warm method — the cost is honest, not free. [[D-021]] is the argument — "validated eagerly and fails at build or start-up rather than at request time" — and it is currently applied only to the misconfigurations that over-trust. A deploy that fails is cheaper than a 401 storm nobody can diagnose, and this is the one item on the list that removes an incident class rather than making it easier to read.

### H-AUTH-12 — A public route, and a stricter check on `/admin`
**Who:** anyone with a health check and an admin area
**Wants:** an optional guard on the catalogue, and a step-up token — different audience, shorter life — on the admin subtree.
**Story:** They mark one guard optional and mount it. For the admin group they build a second, stricter guard and mount it inside the first. Both compile. Every admin request passes.
**Must hold:**
1. An optional endpoint lets an anonymous request through, and still refuses a credential that was presented and does not verify.
2. A second guard mounted inside the first verifies what it was built to verify, or says it cannot.
3. Mounting one guard globally and again on a group costs one verification.

**Today:** ❌ missing for 2 — and it fails silently. 1 holds for a well-formed credential and not for a malformed one. 3 holds.
**Evidence:** For 1: `auth/guard.go:96-100`, pinned by `TestAnOptionalGuardStillRefusesABadCredential` (`auth/guard_test.go:59`), which sends a well-formed `Bearer forged`. A header with no space is not a credential at all (`auth/credential.go:56-61`), so on an optional route `Authorization: eyJhbGci…` without the `Bearer ` prefix — common from SPAs and old clients — takes the anonymous branch and sees the public view, which is the downgrade `auth/guard.go:64-70` says `Optional` prevents. For 3: `auth/guard.go:91`, pinned by `TestASecondGuardDoesNotAuthenticateAgain` (`auth/guard_test.go:68`) and by `TestADoubleInstallAuthenticatesOnce` in all four transports (`auth/http/authnet/middleware_test.go:107`, `auth/http/authgin/middleware_test.go:117`, `auth/http/authfiber/middleware_test.go:122`, `auth/rpc/authgrpc/interceptor_test.go:119`). For 2: that is the *same line*. The check reads whether a principal is present, not which guard produced it, so the inner guard's authenticator, header, lookup and options are never consulted, and the outer guard's ordinary token passes every admin route. All five idempotence tests install the *same* `g` twice — including the Gin and Fiber ones, which are the group-mounting story this case is about — and nothing in the tree covers two different guards. There is no way out from outside either: `auth.WithPrincipal(ctx, nil)` returns the context unchanged (`auth/context.go:22-27`), so a principal cannot be cleared first. `docs/modules/en/auth.md:182` states the idempotence as a feature with no qualification, and [[UC-019]] guarantee 8 says the same.
**If not ready:** the only correct wiring today is not to mount the outer guard on that subtree, which Gin and Fiber groups allow and a global `Use` or a wrapped mux does not, or to call the second authenticator by hand and skip the guard. **The fix should be the default, not an option.** An opt-in `auth.Reauthenticate()` leaves the silence in place for everybody who does not know to type it, and silence is what makes this security-shaped. Key the check on *which* guard produced the principal — a per-guard marker stored beside the principal, or a second context key holding the set of guards that have run — so a second mount of the *same* guard still costs one verification (guarantee 3 and all five tests unchanged) and a second *different* guard always runs. No call site changes. It is a behaviour change, so it has to land before the tag; the authhttp sweep reaches the same conclusion for the same defect, and the opt-in framing this file carried in round 1 was the disagreement, not the severity. **One defect, carried in both sweeps because the story is the bindings' and the fix is `auth.Guard`'s — count it once.** For guarantee 1, the malformed-credential arm needs a decision rather than a patch: refusing a schemeless header on an optional route means `ParseAuthorization` distinguishing "absent" from "present and unusable", which is a third result it does not have.

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

**Today:** 🟡 partial — cookies are reachable and undocumented; query parameters are out of reach.
**Evidence:** The option set is `Header`, `Lookup`, `Optional` and nothing else (`auth/guard.go:46-77`). The guard is handed a header getter, so a `Lookup` reaches a cookie only by asking for the raw `Cookie` header and splitting it — which works, and which no module page mentions. A query string is not a header on any transport, so no getter can see it.
**Two documents put the cookie path in a binding and this case is a challenge to neither.** `auth/doc.go:14-18` says every path that could put a principal into a context — "a header, gRPC metadata, a session cookie" — belongs to a transport, and [[UC-019]]'s Out of scope names "session cookies, CSRF and rate limiting" as different subsystems. Both are about session *management*: minting one, refreshing one, defending one against CSRF. Reading a credential out of a place is what `Header` and `Lookup` already are, and neither of them is a transport. So the option's existence is not the contested part; **its home is**, and this sweep yields it. The authhttp sweep argues for `authhttp.Cookie(name) auth.Option` and the argument wins on the property this module measures: `authhttp` already imports `net/http`, so the parser is `http.ParseCookie` rather than six hand-rolled lines, the three HTTP bindings get it free, and gRPC — where `get("Cookie")` is `""` and a cookie means nothing — never links it. Put it in `auth` and package `auth` grows its first `net/http` import, which is the exact build property [[D-055]]'s forbid gives as its reason. **`auth.Cookie` is withdrawn from this sweep's proposals; `authhttp.Cookie` is the one to build.**
**If not ready:** the cookie is six lines of `Lookup` a consumer has to invent until then; the query parameter is their own binding. The two are different sizes. **The query half is a decision and it is auth's**, because it is about `Guard.Authenticate`'s signature. The cheaper of the two shapes wins: a namespaced key the bindings answer — `get("query:access_token")`, with gRPC answering `""` — keeps the documented `guard.Authenticate(r.Context(), r.Header.Get)` call intact (`docs/modules/en/auth.md:155`), where widening the getter to a source with `Header`, `Cookie` and `Query` methods breaks it. The counter-argument deserves recording: `Guard.Authenticate` is exported, so a hand-written binding that answers the query parameter is about twelve lines, and `test/integration/auth_jwt_test.go:63-69` is a working instance of exactly that shape — which weakens the case for widening the getter at all. The per-binding half belongs to the authhttp sweep, which owns the four getters. What is decided here is whether the getter's shape changes, and it should be decided before the tag because only one of the two candidates is still free afterwards.

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

**Today:** 🟡 partial — the sources are right and the hop is missing.
**Evidence:** `RSA`, `ECDSA` and `EdDSA` (`auth/authjwt/key.go:54`, `:65`, `:73`) each pin their methods, which is the whole point of the type, and the module page lists them in a table (`docs/modules/en/authjwt.md:61-68`). No snippet anywhere shows how to get a `*rsa.PublicKey` from a PEM: `grep -rn "ParsePKIX\|pem.Decode" --include="*.go" .` is empty across the whole repository, and so is `_examples/`. Three of the five exported `KeySource` constructors are covered by no runnable wiring. For 2, `New` panics on a `KeySource` that carries no key (`auth/authjwt/parser.go:102`), which catches a nil source and not a key parsed from the wrong PEM block.
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
**Evidence:** `Guard`'s fields are set by options at construction and only read afterwards (`auth/guard.go:46-77`, `:90-114`). `Parser[C]` holds the key source and a fixed option slice, and `keyfuncFor` closes the request context over per call rather than storing it, with the comment saying that is why (`auth/authjwt/parser.go:179-186`). The JWKS source holds a mutex over the key map, the timestamps and the in-flight channel (`auth/authjwt/jwks.go:113-125`), and the shared-fetch design is pinned by `TestAConcurrentBurstOfMissesIsOneFetch` (`auth/authjwt/jwks_test.go:215`). `apikey`'s authenticator is a struct set at `New` and read after (`auth/apikey/apikey.go:82-112`). Both suites run under `-race`.
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
  and that stays. `JWKSStaleAfter` must not become a fourth panic, an unset value
  must keep today's behaviour, and its default must not be short enough to
  recreate the fetch storm `JWKSMinRefresh` exists to prevent. `HMACAny()` with
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
| An API key in the header the fleet already sends | The documented `auth.Header("X-Api-Key")` refuses every such client, and `AnyScheme()` cannot rescue it; a 4-line `Lookup` is the working spelling | small — after a 401 storm |
| A machine caller in a tenanted service | Both documented examples build a principal with no `Attrs`, which the flagship policy then denies on every request | small — one line of docs |
| A fabricated caller for tests and jobs | One literal, one call | none |
| A real credential in an end-to-end test | `apikey.Static` would do it and nobody here ever has | small, unproven |
| A token for a test of the JWT path | `golang-jwt` in your test module, ~20 lines | small, and foreclosed by documentation |
| A hook that refuses a verified token | Only off `Standard`'s path: 5 lines and 5 names become 11 and 11 | small |
| Roles loaded from my own database | The mapper, which is right — and nothing says it runs per request or what caching it costs | none in code · small in docs |
| A typed claim at the call site | 4 lines per claim, forever | small |
| Two secrets during a rollover | `Custom` plus `golang-jwt` in your imports, or a `Chain` | small |
| Two issuers during a migration | A `Chain`, and nothing anywhere warns about colliding subjects | small · sharp |
| Two audiences during a rename | A `Chain`; `Audience("a","b")` means both-of, and the one-word reach stops checking entirely | small · sharp |
| A static public key from a PEM | `pem.Decode` + `x509.ParsePKIXPublicKey` yourself, shown nowhere in the tree | small |
| Clock skew tolerated | One line that exists, is well documented, and is in no canonical snippet | none in code · small in docs |
| A stricter guard on a subtree | No answer. The inner guard is silently skipped and the principal cannot be cleared | large |
| A credential in a cookie | ~6 lines of `Lookup` you have to invent; nothing documents it | small |
| A credential in a query string | No reach from any binding | large |
| Forwarding the identity to the next service | Your own context key and your own middleware, ~20 lines, in the one place this library said you would not need one | large |
| One guard on HTTP and gRPC at once | Works, is pinned file for file, and is the best thing here | none |
| Building it once and sharing it | Safe, and said nowhere | none in code · small in docs |
| A revoked JWKS key stops being accepted | Only as a side effect of traffic on a *new* key; no maximum age | large |
| A provider outage that looks like an outage | Warm processes survive; every cold one answers 401 to everybody | large |
| Knowing a deploy's auth config is wrong | Nothing until the first request, then a reasonless 401 forever | large |
| Turning auth on without an incident | No shadow mode, and no recipe for the one you can build | large |
| Knowing why a 401 happened | Recoverable in four lines nobody has written for you; reported nowhere | large |
| A permission vocabulary that stays one vocabulary | Nothing links policies to role maps, and two role maps merge by overwriting | large, and it grows |

**Overall:** the contracts are right, and they are the hard part — the reasonless
401, the algorithm pinned to the key, roles expanded once, the three-result key
store, an outage beating a refusal whatever the wiring order, one guard driving
four transports. Every one of those is decided deliberately and pinned by a test
that would fail if somebody undid it. What is not right is the first hour and the
second service. The documented API-key wiring refuses every ordinary client, and
its own escape hatch cannot reach the check that would waive it; `Standard`
against a provider that spells roles anywhere but `roles` produces a principal
full of OAuth scopes and a 403 on everything. Past the first hour the pattern is
consistent: `Standard` has no seam, so the first real requirement drops you to
the long form, and five of the cases above arrive at that same cliff from
different directions. Then the second service arrives and the identity this
library established cannot leave the process. And the module has very little to
say once the system is unhealthy: an outage at the provider, a key withdrawn, a
typo in the audience, a drifted clock, a kid-less token against a two-key set —
five different incidents, one identical silent 401, and no supported way to tell
them apart.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A second guard mounted inside another never runs: the presence check is on the principal, not on which guard produced it (`auth/guard.go:91`), and a principal cannot be cleared (`auth/context.go:22`) | blocker | Silent and security-shaped — a step-up guard on `/admin` verifies nothing and both spellings compile. All five idempotence tests install the same guard twice. The fix is a per-guard marker rather than an opt-in flag, because an opt-in leaves the silence for everyone who does not know to type it — so it is a **behaviour change and must land pre-tag**. **Same defect as the authhttp sweep's blocker 1, and now the same timing; count it once.** |
| 2 | An unreachable or failing JWKS endpoint renders as 401, not as a server fault (`auth/authjwt/parser.go:169`) | blocker | It is the confusion `apikey.Store`'s three results exist to prevent, one package away. A warm process survives; every cold one — a deploy, a restart, an added replica, a rotation — tells every client its credentials are bad while the 5xx rate stays flat. Changes a status code, so **decide it before the tag.** |
| 3 | `auth.Header("X-Api-Key")` refuses every client that sends a bare key, `AnyScheme()` cannot rescue it because `ParseAuthorization` runs first (`auth/credential.go:56-61` from `auth/guard.go:123`), and `apikey` is exercised nowhere outside its own package test | serious | The documented setup for one of the two shipped authenticators does not work against an ordinary API-key client, the page's own escape hatch does not reach, and nothing in `test/` or `_examples/` would have caught it. Documentation half is free today; `apikey.Header` is additive. |
| 4 | `authjwt.Claims` reads roles from `roles` only and takes the OAuth `scope` string as permissions verbatim (`auth/authjwt/claims.go:24`, `:142-146`), and nothing says so at the call site | serious | A Keycloak or Cognito token yields a principal holding `openid profile email` — permissions that look real, match no policy, and 403 every request with no reason anywhere, on day one. `scp` is a third permission spelling nothing reads. |
| 5 | No supported way to forward the caller's credential to the next service: `Guard.Authenticate` drops it and `auth.WithCredential`/`CredentialFrom` do not exist | serious | Every two-service consumer invents a second context key and a second middleware — the thing this library's opening promises they will not need. The remote sweep already writes `auth.CredentialFrom` into its ideal call site. A second answer to "where does identity live" is free only before the tag, and it needs a yes or no against [[D-055]]. |
| 6 | A JWKS key set has no maximum age: `jwks.fetched` is written at `auth/authjwt/jwks.go:222` and read nowhere | serious | A withdrawn signing key keeps verifying until some other token names an unknown kid; withdraw one without introducing one and the process trusts it until it restarts. `JWKSStaleAfter` is a two-line change; its default and its stale-on-failure behaviour must be decided with blocker 2, since the two pull against each other. |
| 7 | Nothing tells a consumer before the first request that the auth configuration cannot work (`auth/authjwt/jwks.go:41-47` panics only on an empty URL) | serious | A typo in the key-set URL, a blocked egress rule or a wrong issuer is a reasonless 401 on every request at every replica, forever. The fix is `Parser.Warm(ctx) error` and **not** a `Warm()` option inside `JWKS`, which can only panic and would turn a provider outage into a fleet that fails to boot. |
| 8 | Three tests that read as pins and assert almost nothing: `TestAKeySetThatCannotBeReachedIsARefusalAndNotAPanic` (`auth/authjwt/jwks_test.go:148`) and `TestAMapperMayRefuseATokenThatVerified` (`auth/authjwt/claims_test.go:122`) assert only `err != nil`; the `Chain` refusal subtest (`auth/credential_test.go:81`) asserts the sentinel and nothing about disclosure | serious | Each names a classification it does not check, and blocker 2's behaviour is one of them — a reviewer asking whether the choice is deliberate is told yes by a name and nothing by the assertion. `auth/apikey/apikey_test.go:83` is the standard. |
| 9 | No exported way to recover a refusal's reason, and nothing reports it (`auth/errors.go:33`; `errs.Fault.Error()` is classification only) | serious | [[UC-019]] guarantee 6 promises recoverable and delivers it; nothing promises reported. Auth's half is `auth.Reason(err)` **and** `auth.Refuse(cause error)` — the string-only version breaks `errors.Is` for the consumer's own refusals. The log line is the authhttp sweep's blocker 4 — one incident, two halves, count it once. |
| 10 | [[UC-019]] and `docs/ai/usecases/Index.md:90` say "covered — every guarantee is pinned", and two are not: guarantee 8 is defeated for a second, different guard (blocker 1) and guarantee 11's only pin asserts `err != nil` (blocker 8) | serious | A tag freezes the artefact the next agent trusts instead of re-deriving. The use case and the Index row are what is wrong here, not the code, and both need the same change: the Status paragraph naming guarantees 8 and 11, and the Index status flipped. |
| 11 | A nested or namespaced claim has no convenience form and the hand-written extractor carries a fail-closed obligation written nowhere (`crud/decorators/security/principal.go:169-186`) | sharp edge | The route exists — a `security.ScopeField` closure — and is in no document, so the version a consumer writes reintroduces `WHERE tenant_id = 0`. `ScopeAttrPath` is the fix and it belongs in `security`, not on `Attr`. The request-time panic on a type mismatch is the security sweep's blocker 5 — count that half once. |
| 12 | `authjwt.HMAC` takes one secret, `Issuer` one issuer, and `Audience` means all-of | sharp edge | The only one-line answers to "two issuers this month" and "two audiences this month" are `AllowAnyIssuer()` and `AllowAnyAudience()`, which stop checking. `AnyIssuerOf` also has to widen the start-up check at `parser.go:111`; the `HMAC` half wants `HMACAny` rather than a variadic, because a variadic breaks the function value and a zero-argument call passes `valid()`. Nothing warns that two issuers can mint the same subject. |
| 13 | A credential in a query string is unreachable from any binding; the cookie helper has no owner yet | sharp edge | Event streams cannot set headers. The cookie half is settled in this round — it goes to `authhttp`, which already imports `net/http`, and this sweep withdraws `auth.Cookie`. The query half is a decision and must be taken before the tag, because widening `Guard.Authenticate` breaks a documented call and a namespaced key does not. |
| 14 | `apikey` has no key generator and no caching seam, and its documented principal names no tenant | sharp edge | The mint half of the ten lines is undocumented while the hash half is; the first caching wrapper a consumer writes will trade revocation away without saying by how much; and a partner key copied from the page is denied by the flagship row filter. |
| 15 | Nothing links the permission strings a `RoleMap` grants to the ones twenty policies name, and two `RoleMap`s merge by overwriting | sharp edge | A typo, or one role defined in two feature packages, fails closed as a permanent 403 on one verb of one resource with no reason on the wire and none in a log. The enumerator and `Merge` are auth's and additive; **the check is `security`'s and is that sweep's blocker 6 — count it once.** |
| 16 | Three of the five `KeySource` constructors have no PEM hop and no runnable wiring: `grep -rn "ParsePKIX\|pem.Decode"` over the tree is empty | sharp edge | An internal issuer publishing one public key is an ordinary deployment, and the four lines it needs are shown nowhere; reaching for `ParseCertificate` instead produces the same reasonless 401 as everything else here. |
| 17 | [[FL-019]]'s line numbers have drifted through `parser.go`, `claims.go`, `credential.go` and `authhttp.go` | sharp edge | A flow doc's one job is to say where something is. Step 5 → `credential.go:55` (`:56`); step 7 → `credential.go:36` (`:37`); step 8 → `parser.go:137` (`:161`) and `parser.go:110` for the method pinning (`:127`); step 9 → `parser.go:171` (`:195`); step 10 → `claims.go:121` (`:137`); step 14 → `authhttp.go:66` (`:67`). |

**Timing.** Five of these are not free after a tag. Blocker 1 is a default
change, not an option — an opt-in preserves the silence, so it is pre-tag or it
is v2. Blocker 2 changes a rendered status code. Blocker 5 adds a second context
key to `auth`, and a later one is a second answer to the same question. Blocker
6's default changes what existing deployments do. Blocker 13's query shape either
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
