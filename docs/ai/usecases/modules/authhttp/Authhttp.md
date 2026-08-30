# authhttp · authnet · authgin · authfiber · authgrpc — establish who is calling, at the door, on whichever transport the service already speaks

**Covers:** `github.com/frostgrove/vv/auth/http/authhttp`, `github.com/frostgrove/vv/auth/http/authnet`, `github.com/frostgrove/vv/auth/http/authgin`, `github.com/frostgrove/vv/auth/http/authfiber`, `github.com/frostgrove/vv/auth/rpc/authgrpc`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — frozen HTTP mounts, repeated-credential selection,
multi-source credential shape and Fiber refusal drift remain. The guard boundary
itself now validates at transport construction, executes distinct guards, and
fails closed on assurance-ambiguous A -> B -> A composition.

This file is the binding half. What a credential is, how a token is parsed, and
what options `auth.Guard` should grow belong to the sibling sweep at
`docs/ai/usecases/modules/auth/Auth.md`, and are cross-referenced rather than
re-argued here. What is this file's: which getter each binding hands over, what
each framework does with a refusal nobody asked it about, where a middleware has
to sit relative to the door, and which of the four mounts can still be changed
after a tag.

## What a consumer is actually trying to do

Someone has a service with rows in it that belong to different people. They have
already decided how identity is issued — an identity provider, an API key table,
a mesh that terminates mTLS one hop earlier. What they want here is the boring
middle of that: take whatever the caller presented, decide whether it is real,
and make the answer available everywhere further in without threading a
parameter through four layers.

The reason they want it at the door rather than in each handler is that the
rules are not in the handlers. The row filter that keeps tenant 7 from reading
tenant 8 lives next to the repository, several calls away from anything that has
ever seen a request. So "who is calling" has to survive the trip, and it has to
survive it the same way whether the call arrived over a browser fetch, a mobile
client, or an internal RPC from another service in the same cluster.

Almost nobody arrives with a greenfield service. They have an API that is
already live, with clients they cannot redeploy on the same afternoon, and the
first thing they need is a week where a missing or bad credential is recorded
and still served. Only after that week does the door start refusing. A library
that assumes the service was authenticated from day one has skipped the step
that decides whether it gets adopted at all.

Nobody's service is uniformly protected either. The shape every product API
converges on is four requirements at once: `/healthz` open, `/catalogue` richer
when you are signed in, `/orders` closed, `/admin` closed harder. They mount one
thing globally because that is the safe default, and then spend a week finding
out which of the four exceptions the framework will let them express.

On an ordinary Tuesday the questions are small and specific. The orchestrator
kills the pod because the liveness probe got a 401. The browser SPA reports a
CORS error that names no cause, because the preflight it sent carried no
credential and never will. The login endpoint cannot require the thing it
issues. The batch fleet sends a key in a header nobody spells `Authorization`,
and it hits the same routes the browser does. The event stream cannot set a
header at all. The admin area needs a fresher credential than the rest of the
API, and one `/exports` route needs a permission with no repository behind it to
check one.

Two more things they expect and rarely say out loud. First, a refused request
should look like every other failure the service returns — same envelope, same
machine code, same language — because the client has one error path. Second,
when a rollout goes wrong and every request starts coming back refused, somebody
on call has to be able to find out *why* from the server side, while the client
is still told nothing. And when the requests succeed, the identity that made
them succeed has to reach the access log, the trace and the metric labels,
because "which tenant is causing this" is the first question asked of any
dashboard — from the same log line that also has to appear for the requests that
were refused.

And underneath all of it: the refusal must not depend on remembering to install
a second thing. A door that silently opens when a piece of wiring is missing is
worse than no door.

## Happy cases

### H-AUTHHTTP-01 — Put the middleware in front of the API I already have
**Who:** the author of a Gin service with a dozen routes and a repository behind them
**Wants:** every request carrying a verified caller, without touching a handler
**Story:** They build the thing that verifies tokens, wrap it in a guard, and add one line where the middleware chain is declared. Handlers do not change. The repository's policy starts seeing a caller.
**Must hold:**
1. Mounting is one statement on the three HTTP bindings.
2. A request with no credential never reaches a handler.
3. A middleware built with nothing to authenticate against does not start.
4. The identity reaches the *repository*, not only the handler — on every transport.
**Today:** 🟡 partial — 1, 2 and 3 hold, including zero-value validation; 4 is proven in two halves that never meet
**Evidence:** `auth/http/authgin/authgin.go:41-56`, `auth/http/authnet/authnet.go:43-58`, `auth/http/authfiber/authfiber.go:43-58`, `auth/rpc/authgrpc/interceptor.go:48-59`. The context is written back where a repository will see it — `c.Request.Context()` in and `c.Request = c.Request.WithContext(ctx)` out on Gin (`authgin.go:47`, `:54`), `c.SetContext` and not `Locals` on Fiber (`authfiber.go:55`), a replacing `ServerStream` on gRPC (`interceptor.go:128-136`). gRPC's getter is case-folded metadata, so the guard's default `Authorization` finds what a client sent as `authorization` (`interceptor.go:109-126`, `TestTheMetadataKeyIsFoundWhateverItsCase`). Every constructor calls `Guard.Validate`; `TestANilGuardRefusesToStart` carries nil and `new(auth.Guard)` through net/http, Gin, Fiber, Unary and Stream. Direct low-level Authenticate returns an internal fault wrapping `ErrGuardNotReady`, not a request-time panic. For 4: every binding's `TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` stops at a fake handler, and the one test that reaches a live database — `test/integration/auth_jwt_test.go:57-72` — calls `Guard.Authenticate` directly with a synthetic getter and never touches a binding. No test on any transport walks request → middleware → handler → repository → SQL.
**If not ready:** nothing to write; the join is structural and almost certainly sound. But it is the module's whole purpose and it rests on construction, on a security-shaped guarantee, so it should be closed by one integration test that mounts a binding in front of a CRUD handler over a gated repository. Round 1 said `authnet` was the only place that costs no new dependency; that is true for a *satellite unit* test and wrong for the integration module, which already carries `crudgin`, `crudfiber` and `crudgrpc` as requirements and `authgin`, `authfiber` and `authgrpc` as `replace` lines (`test/go.mod:97-99`, `:118-122`). All four are free there — and Fiber is the one to write, because Fiber is where `SetContext` versus `Locals` is a live trap.

### H-AUTHHTTP-02 — The browser sends a preflight before it sends anything
**Who:** the author of an SPA-backed API, on the first afternoon a browser calls it
**Wants:** the CORS preflight to survive a globally mounted door
**Story:** They mount the middleware over everything, deploy, and the SPA stops working. The network tab shows a CORS error with no status attached. The browser sent `OPTIONS /orders` with `Origin` and `Access-Control-Request-Method` and — by specification — no credentials at all. The door answered 401 with no `Access-Control-Allow-*` headers, so the browser reported the missing headers and never mentioned the 401.
**Must hold:**
1. A preflight is not refused for presenting no credential.
2. If the answer is "put your CORS middleware outside the door", that is written down where somebody wiring the door will read it.
3. No exemption mechanism is offered as the answer to a preflight, because a preflight hits every path and an exemption list that can express that is a wildcard.
**Today:** ❌ missing — nothing anywhere in the tree knows what a preflight is
**Evidence:** `grep -rni "cors\|preflight"` and `grep -rn "MethodOptions"` over the whole repository both come back empty — no code, no test, no module page, no flow. A required guard refuses a request that presents no credential at `auth/guard.go:100`; the `if g.optional` branch two lines above (`:97-99`) is the only exception, and an SPA does not mount an optional door on `/orders`. Every binding mounts in front of routing, so `OPTIONS` reaches it like anything else. The one auth example mounts globally (`_examples/auth-jwt-gin/main.go:152`) and has no browser client, so it never meets this.
**If not ready:** the fix is ordering, and it is one line: register the CORS middleware before the auth middleware in the same chain, so the preflight is answered and short-circuited before the door sees it. That works on all three HTTP frameworks today. What is missing is anybody saying it — each binding's page, both usage guides, and the ordered chain in the example. Must-hold 3 is stated as a refusal on purpose: it is the property this sweep will not trade, and the argument is in Contested.

### H-AUTHHTTP-03 — Keep the health check, the metrics endpoint and the login route public
**Who:** the same author, the day the orchestrator starts restarting the pod
**Wants:** three paths exempt from a middleware mounted over everything
**Story:** They mounted the middleware globally because that is the safe default. Now `/healthz` answers 401, the liveness probe fails, and the login endpoint refuses to issue the token it is asked for.
**Must hold:**
1. A named route can be exempt without unmounting the middleware everywhere else.
2. The exemption is an exact list a reviewer can audit, not a prefix that widens on its own.
3. Whatever the answer is, it is the same word on all four transports.
4. What the list is matched against is stated per binding, and is the router's resolved route wherever the framework gives a middleware one.
5. A request that matches no route at all has a stated answer, because a global door answers it too.
**Today:** 🟡 partial — gRPC has 1 and 2, the three HTTP bindings have neither, and 3, 4 and 5 hold nowhere
**Evidence:** gRPC has `Skip(fullMethods...)`, exact names only, with the reason spelled out — `auth/rpc/authgrpc/interceptor.go:19-37`, pinned by `TestSkipLeavesTheNamedMethodAlone` including a control that a prefix is refused (`interceptor_test.go:144-176`). It is auditable because `info.FullMethod` is handed back verbatim by grpc-go and no client chooses it. The three HTTP bindings have no equivalent: `Middleware(g, ...porthttp.RenderOption)` and nothing else (`authnet.go:43`, `authgin.go:41`, `authfiber.go:43`). The escape hatch on `net/http` is per-route mounting, which the package documentation leads with (`authnet.go:5`) and `authnet.Handler` exists for (`authnet.go:60-63`); on Gin and Fiber it is a group. For 5: on Gin `c.FullPath()` is `""` for a request that matched no route (`gin@v1.12.0/context.go:177-179`), so a globally mounted door answers 401 rather than 404 for every typo'd URL — arguably correct, since route existence is not leaked, and written down nowhere. `authgrpc.md:84-85` states the mirror image for gRPC ("There is no 404 to lose to") and the HTTP half is stated nowhere.
**If not ready:** per-route mounting costs almost nothing to write on all three — the real objection is that it fails *open* on the route somebody forgets, which is the failure this whole module exists to make impossible. That is the argument for the option, and line count is not. What the option needs settling first is must-hold 4, and each framework answers differently: Gin gives a middleware the resolved pattern (`c.FullPath()`), Fiber does not — under `app.Use` the middleware's own Use route is what `c.route` holds (`fiber/v3@v3.4.0/router.go:257`, `ctx.go:366-378`), so `c.Route().Path` is `/` and not the endpoint — and `net/http` exposes `http.Request.Pattern`, but `ServeMux` fills it *after* routing, so a middleware wrapping the mux can never read it. Two of the three therefore match a normalized path, which is why the matcher belongs in one place rather than three. The shape is in "Why this shape"; it does not fit the current signature, which is H-AUTHHTTP-06's.

### H-AUTHHTTP-04 — Public, optional, required, stricter, and two credential kinds — in one service
**Who:** anyone shipping a product API rather than a demo
**Wants:** `/healthz` open, `/catalogue` richer when signed in, `/orders` closed, `/admin` closed harder, and `/exports` reachable by both the browser and the batch fleet
**Story:** They read the mechanisms — exempt a route, mark a guard optional, mount a second guard — and try to compose them. Some combinations work, one works for a reason nobody wrote down, one is impossible, and the one they reach for to accept two credential kinds is the wrong shape entirely.
**Must hold:**
1. An optional guard globally with a required guard on a subtree refuses an anonymous request to that subtree.
2. A required guard globally with an optional subtree inside it is possible.
3. Accepting two kinds of credential on one mount has one documented answer, and the composition a consumer reaches for is either it or ruled out by name.
**Today:** ❌ missing for 2 and 3; 1 holds deliberately and is pinned
**Evidence:** 1 works in both forms. An optional outer guard with no credential
returns the context unchanged, so the required inner guard runs. If the outer
guard did authenticate, the inner guard still runs because idempotence is keyed
to the concrete guard instance rather than to the presence of any principal
([[D-076]]). `TestDifferentGuardsAuthenticateIndependently` pins this through
all four transports, with `TestADoubleInstallAuthenticatesOnce` as the same-guard
control. 2 is impossible: the outer guard has already written the refusal
(`authnet.go`, `authgin.go`, `authfiber.go`) and the inner middleware never runs.
For 3, the answer is one guard with `auth.Lookup` plus `auth.Chain` — `Auth.md`
H-AUTH-06 scores it ✅ and the two-credential example is in `auth.Lookup`'s own
doc comment. Two stacked guards are cumulative checks, not alternative
credentials, and the binding module pages now say when a different guard runs.
**If not ready:** for 2, the shape is per-framework and structural — group the optional subtree outside the global mount rather than inside it, which under a global `Use` or a wrapped mux means not mounting globally at all. That is worth a paragraph in the usage guides, because the reader who reaches for `Optional()` on a group has already mounted globally and the option will silently do nothing. For 3 it is one sentence on each binding's page: two kinds of credential are one guard with a `Lookup` and a `Chain`, never two mounts.

### H-AUTHHTTP-05 — A stricter check on the admin subtree
**Who:** a SaaS author whose `/admin` routes need a step-up token — different audience, shorter life, sometimes a different header
**Wants:** the ordinary guard everywhere, a second and stricter one under `/admin`
**Story:** They mount the API guard globally and add the admin guard on the admin group. Both compile. Every admin request passes. They assume the step-up token is being checked.
**Must hold:**
1. A second guard mounted inside the first actually verifies what it was built to verify.
2. If it cannot, that is loud rather than silent.
**Today:** ✅ handled
**Evidence:** A successful guard records its exact `*Guard` and the principal
state it installed in an immutable request-context marker chain. A consecutive
repeat is idempotent; a different guard authenticates and replaces the
principal only after it succeeds. An older A reached after B returns an internal
fault wrapping `ErrAmbiguousGuardOrder`: the framework cannot know whether B is
a step-up or a downgrade. The intended admin arrangement A -> B runs both
checks and ends at B. Both `ordinary -> step-up -> ordinary` and inverse
`strict -> weak -> strict` fail before the handler. Core tests pin both
directions. Unary and all HTTP bindings pin A -> A/A -> B; Stream additionally
pins idempotence, final principal and both re-entry directions on its replaced
context path ([[D-076]], [[UC-019]] guarantee 8).

### H-AUTHHTTP-06 — Make the 401 look like every other error this API returns
**Who:** a team with a published error contract and a client that branches on a machine code
**Wants:** the refusal at the door rendered through their vocabulary and their message catalogue
**Story:** They already replaced the body their CRUD handlers return. They pass the same renderer to the auth middleware and it does not compile.
**Must hold:**
1. The refusal carries a machine code and a message, in the shared envelope.
2. It renders in the request's language.
3. It says nothing about which half of the credential was wrong.
4. It is written even when no error middleware is installed.
5. Their *codes* and their *message catalogue* reach it.
6. A wholesale renderer — RFC 9457, say — is a value they can give this binding, as it is a value they give the CRUD binding.
**Today:** 🟡 partial — 1, 3, 4 and 5 hold; 2 holds on two of three HTTP bindings and is unproven on the third; 6 does not, and the signature that blocks it is about to be frozen
**Evidence:** `authhttp.Refuse` (`auth/http/authhttp/authhttp.go:67-92`) renders through `porthttp`, adds every header the renderer asked for, and writes rather than defers (the reason is at `authhttp.go:10-16`). 5 already holds and is easy to overstate as missing: `porthttp.RenderOption` carries `WithCodes` and `WithMessages` (`port/porthttp/render.go:51`, `:57`), and `authnet.md:71-72` promises exactly that. Locale from `Accept-Language` (`authhttp.go:49-54`), pinned by `TestARefusalIsRenderedInTheLanguageTheRequestAskedFor` (`authhttp/refuse_test.go:195`) — **but Fiber does not go through `Refuse` at all.** It has its own `locale()` (`authfiber/locale.go:17-23`) and its own header-and-body writer (`authfiber.go:60-73`), and no test in the tree exercises either: `authfiber/middleware_test.go:83` checks the body and the `Content-Type` and nothing else, and `grep -rn "Accept-Language" auth/` finds `authfiber/locale.go` and no Fiber test. For 6, the two seams are compatible and the mount is not: `crudhttp.Renderer` is a type *alias* for `porthttp.Renderer` (`crud/http/crudhttp/porthttp.go:23`) and `Refuse` takes `porthttp.Renderer` (`authhttp.go:67`), so the value passed to `crudnet.WithRenderer` is exactly the value `Refuse` accepts — but all three HTTP bindings take `...porthttp.RenderOption`, a variadic of the *EnvelopeRenderer*'s option type, which no `Renderer` can be smuggled through (`docs/api/surface.md:61-62`, `:731`, `:736`).
**If not ready:** the renderer escape hatch is real on two bindings and absent on the third, but its repair belongs to the shared Port/Crudhttp/Crudgrpc renderer contract, not a binding-local `WithRenderer`. `authhttp.Refuse` is exported and `authhttp.md:17-18` names "render a refusal yourself" as a reason to import the package; Fiber needs the same shared refusal adapter rather than a hand-written locale/header/body copy. The HTTP-specific release choice is smaller: add `authgin.New(guard, opts ...Option)` beside the frozen middleware constructor, carrying `Skip(...)` only. That adds the route-exemption door without creating a fourth renderer precedence chain.

### H-AUTHHTTP-07 — A standards-checking client wants `WWW-Authenticate` on the 401
**Who:** a team publishing an OpenAPI document, or one whose clients use an OAuth2 library that refreshes on a challenge
**Wants:** the header RFC 7235 says a 401 carries
**Story:** The API passes their conformance suite everywhere except the door. Their client library never attempts a refresh, because the branch it takes on a 401 is keyed on the challenge header being present, and it is not.
**Must hold:**
1. If the header is deliberately omitted, the reason is written where a consumer will find it before filing a bug.
2. A consumer who needs it can add it without replacing the refusal.
**Today:** 🟡 for 1 · 🟡 for 2
**Evidence:** the omission is deliberate, argued in two places (`authhttp.go:61-66`, `docs/modules/en/authhttp.md:41-50`) and control-cased: `authhttp/refuse_test.go:121` fails if a `WWW-Authenticate` nobody asked for is ever invented, which is what turns the ✅ from an assertion into a proof. This sweep endorses the argument — a `Basic` challenge makes a browser open a modal login box no API wants, and a bearer challenge's `error=` parameter exists precisely to name which part of the token was wrong, which is [[D-056]]'s whole subject. 1 is 🟡 rather than ✅ only because of where it is written: a consumer wiring Gin reads `authgin.md`, and `grep -rn "WWW-Authenticate" docs/modules/en/auth{net,gin,fiber}.md` is empty. For 2, the stated recourse is "sets it in a wrapper", which is nowhere shown and nowhere tested. It is also awkward in the one direction that matters: `Refuse` writes the response itself, so a wrapper must set the header on the `ResponseWriter` *before* the middleware runs, not after, and a wrapper that sets it unconditionally puts a challenge on every 200.
**If not ready:** the honest workaround is a `ResponseWriter` that defers `WriteHeader` and adds the header when the status turns out to be 401 — about fifteen lines, and the kind of thing that is written wrong the first time. A documented four-line recipe in `authhttp.md`, plus one line on each binding's page saying the header is absent on purpose, costs nothing and removes the whole class. The header is not a candidate for the render options: it is a fixed string per deployment, and a renderer already returns an `http.Header` that `Refuse` copies verbatim (`authhttp.go:70-74`) — so a consumer replacing the renderer per H-AUTHHTTP-06 gets it on `net/http` and Gin for free. Not on Fiber: `Refuse` uses `w.Header().Add` (`authhttp.go:72`) where the Fiber twin uses `c.Set` (`authfiber.go:66`), which *replaces*, so a two-challenge `WWW-Authenticate` keeps both values on two bindings and only the last on the third. Nothing pins that difference.

### H-AUTHHTTP-08 — Find out why every request started coming back refused
**Who:** whoever is on call during a rollout that changed the audience by one character
**Wants:** the reason, on the server, while the client keeps being told nothing
**Story:** The dashboard shows 401 on everything. The bodies say `unauthenticated` and nothing else, which is correct. They go looking for a log line naming an audience mismatch and there is not one.
**Must hold:**
1. The reason never reaches the client.
2. With a stock request logger installed and no library-specific code, a refused request produces one server-side line naming the check that failed — and the same is true on all four transports.
**Today:** 🟡 partial — 1 holds everywhere, 2 holds on none of the four
**Evidence:** the reason exists — `auth.Unauthenticated(reason)` keeps it in the wrapped error ([[D-056]]) and every refusal path carries it. What each framework does with it:

| Binding | What happens to the cause | Where |
|---|---|---|
| `authgin` | filed with `c.Error(err)`, so Gin's own logging middleware sees it — but only a consumer running `gin.Logger()` or their own reader of `c.Errors` | `authgin.go:49`, pinned by `TestTheCauseIsFiledWithGinsErrorBag` (`authgin/binding_test.go:23`) |
| `authgrpc` | returned, so an interceptor chained *inside* the renderer sees it | `interceptor.go:56`, `:75` |
| `authnet` | dropped — `Refuse` is called and the handler returns | `authnet.go:52-53` |
| `authfiber` | dropped harder — `return refuse(...)` returns the *write* result, so Fiber's own error handler never sees the cause | `authfiber.go:53` |

Round 1 wrote must-hold 2 as "each binding does something with the error", which `c.Error` satisfies while an on-call engineer still sees nothing. Restated as the log line, Gin fails it too. The gRPC row needs its qualification: `crudgrpc.Errors` replaces the fault with `rd.Render(...).Err()` (`crud/rpc/crudgrpc/interceptor.go:36`), and a `*status.Status`'s error does not wrap the original — so under the documented wiring `ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard))` (`docs/modules/en/authgrpc.md:56`), `Errors` is outermost and a logging interceptor placed beside it sees the rendered 401 and nothing else. The ordering that keeps the reason is the opposite of the intuitive one. The only `port.Logger` calls in the shared half are for a refusal that would not encode or would not write (`authhttp.go:83`, `:90`) — never for the refusal itself.
**If not ready:** whether the reason should be reachable *at all* from outside the process — an exported `auth.Reason(err) string`, for a metric label or an audit row — is `auth`'s question and `Auth.md` H-AUTH-17 proposes it. This file's half is the log line, and it needs three decisions the proposal usually ducks. The level: one `Error` line per unauthenticated request turns an ordinary credential-stuffing run into a log flood, and [[D-062]]'s existing sites are all "this library's own bug" — so `Debug`, with the reason and never the credential. The place: putting it in `authhttp.Refuse` makes Gin report the same refusal twice, because `authgin` already files it with `c.Error`. Either the line goes in each binding's refusal path with Gin abstaining, or it goes in `Refuse` and `authgin` drops `c.Error` — and Fiber does not import `port` today, so closing it there is that import or a shared helper in `authhttp`. The third is H-AUTHHTTP-11's and inherited: with no middleware installing a request-scoped logger, every one of those lines lands on `slog.Default()`.

### H-AUTHHTTP-09 — The identity provider is down, and the door is holding the caller's deadline
**Who:** the same on-call engineer, an hour earlier, before anybody knew it was the provider
**Wants:** an outage to look like an outage
**Story:** The provider's JWKS endpoint stops answering. Every request comes back 401. Users are signed out across the fleet, the 5xx rate stays flat, and nothing pages.
**Must hold:**
1. *(inherited, not scored here)* A provider that is unreachable is not reported to every caller as a bad credential.
2. The network call the door makes on the first request after a rotation does not inherit an arbitrary client's timeout.
**Today:** ❓ unverified and probably not, for 2
**Evidence:** 1 is `authjwt`'s and it is `Auth.md`'s **blocker 2** — `Parser.Parse` turns every error out of `jwt.ParseWithClaims`, including a JWKS fetch failure raised through `keyfuncFor`, into `auth.Unauthenticatedf("token rejected: %v", err)` (`auth/authjwt/parser.go:169`, keyfunc at `:186`). `auth.Chain` then classifies it as a refusal (`auth/credential.go:107`) and the door answers 401. It is named here because this is where the consumer meets it — four transports, all rendering that refusal as a 401 with no reason — and it carries no verdict here. 2 is this file's: every binding hands the guard the *request's* context (`authnet.go:50`, `authgin.go:47`, `authfiber.go:51`, `interceptor.go:54`, `:73`), and the parser closes that context over the key fetch on purpose (`parser.go:182-187`), so the first request after a key rotation pays for the refresh on its own deadline. A client with a one-second timeout can cancel that fetch, and every replica that restarts is a cold one. Nothing in the tree tests it, and `grep -n "timeout\|deadline"` over the four bindings finds nothing.
**If not ready:** for 2 the shape is a key source that refreshes on a context of its own rather than the caller's, which is `authjwt`'s to decide. What belongs here is that the bindings offer no seam for it — there is nowhere between the request context and `Guard.Authenticate` for a consumer to put a `context.WithTimeout`, short of the hand-written binding of H-AUTHHTTP-06, which does not exist on Fiber.

### H-AUTHHTTP-10 — gRPC: a refused stream that says it was refused
**Who:** the author of an internal service with unary methods and one long-lived stream
**Wants:** a refusal a client can branch on, both ways
**Story:** They wire the interceptors the way the page shows. A client with no credential gets `UNAUTHENTICATED` on a unary call and `Unknown` on the stream.
**Must hold:**
1. A refused stream answers the same code as a refused unary call.
2. The wiring that is documented is the wiring that produces it.
**Today:** 🟡 partial — the fix exists and is named in no document
**Evidence:** the refusal is an unrendered fault returned from the interceptor (`interceptor.go:56`, `:75`); nothing in the tree implements `GRPCStatus()`, so something downstream must turn it into a status. For unary calls that is `crudgrpc.Errors`; for streams it is `crudgrpc.StreamErrors`, whose own doc comment names this exact bug — *"there was no downstream for a stream. grpc-go then wrapped the bare error as codes.Unknown"* (`crud/rpc/crudgrpc/interceptor.go:40-53`). `StreamErrors` appears in no module page, no flow and no README; only in `docs/api/surface.md:834`. `docs/modules/en/authgrpc.md:57` shows `grpc.ChainStreamInterceptor(authgrpc.Stream(guard))` with nothing else in the chain, which is the broken wiring. The test that would have caught it asserts the classification rather than the code: `TestAStreamIsAuthenticatedWhenItOpens` checks `errors.Is(err, auth.ErrUnauthenticated)` (`interceptor_test.go:202`), which is true while the wire answer is `Unknown`; the unary twin at `interceptor_test.go:89-91` accepts either code on purpose, because this package writes no status. **Same bug as `Crudgrpc.md` blocker 4** — one bug, two sweeps; `Crudgrpc.md:496` already assigns the `authgrpc.md` line to this file and keeps the `crudgrpc.md` line, so the ownership is settled and the count is one.
**If not ready:** one call added to the stream chain fixes a consumer's build; nothing tells them to add it. Underneath is a decision-compatibility problem, and round 1 attributed it to the wrong document. It is not [[FL-019]]'s reading and it is not [[D-051]], which says nothing about `auth` at all — it is [[D-055]]'s own **What it forbids**: *"do not make an `auth*` module require its `crud*` sibling."* `authgrpc.md` contradicts itself about this on one page: `:11` promises "It does **not** require crudgrpc ([[D-051]])" and `:79-82` says "A refusal is an error returned from the interceptor; `crudgrpc.Errors` renders it". A consumer taking `authgrpc` without `crudgrpc` gets `Unknown` for every refusal, which is `authhttp.go:10-16`'s own rule broken on the one transport it does not cover: the failure mode of the door was open must not depend on a second thing being installed, and here it does. Either `authgrpc` grows its own status for a refusal, or the promise on `:11` comes off. Two consumer-facing pages also cite D-051 for a rule it does not carry (`authgin.md:11-12` and `:96`, `authfiber.md:11`, `authgrpc.md:11`, and the comment at `authnet/binding_test.go:19`); all five want D-055 instead.

A second, smaller thing, and it is in the code rather than missing from it: a
stream is authenticated when it opens and never again
(`interceptor.go:62-66`). A credential that expires mid-stream keeps serving.
Round 1 called this undocumented and was wrong — it has its own headed section
on `authgrpc.md:105-109` and its own paragraph in `Stream`'s doc comment, and
[[UC-019]] lists it out of scope. It is recorded here only so the next reader
does not re-find it.

### H-AUTHHTTP-11 — One access log that carries the tenant *and* the refused requests
**Who:** whoever owns the dashboard, on the day "which tenant is causing this" is first asked
**Wants:** one middleware that logs every request with a `tenant` label and a status column
**Story:** They add a `tenant` label to their request logger. It is empty on some lines. Then they notice the 401s have no lines at all.
**Must hold:**
1. A logging or tracing middleware can see the principal.
2. The same middleware also runs for a request the door refused.
3. Where it has to be mounted relative to the door is stated, because it is not the same answer on every framework.
**Today:** 🟡 partial — 1 and 2 are satisfiable together on Gin and gRPC, on neither of the other two, and 3 is written nowhere
**Evidence:** the accessor is transport-neutral and correct: `auth.PrincipalFrom` / `auth.Require` on the request context (`auth/context.go:30-51`), and on Fiber it is `c.Context()` precisely because the middleware wrote there and not to `Locals` (`authfiber.go:55`). Round 1 got Gin backwards and it is worth stating the right way round, because the correction changes the advice. `c.Request` is a field on the shared `*gin.Context`, not a value copied down the chain, so a logger registered *before* the door and logging after `c.Next()` returns — which is where every Gin logger sits — reads the replaced request and sees the principal; and it still runs for a refused request, because `c.Abort()` stops handlers not yet entered and does not unwind the ones already inside their `c.Next()`. The repository's own `authgin/binding_test.go:27` is that arrangement and it observes post-auth state. Only a Gin logger that captures `c.Request.Context()` *before* calling Next is blind. Fiber works the same way for must-hold 1 — `SetContext` writes into the fasthttp request's user values, which an outer middleware reads back after `c.Next()` — but fails must-hold 2 for the same reason `authnet` does: on refusal the middleware returns `refuse(...)` and never calls `c.Next()`, so an *inner* logger never runs, and an outer logger has no cause to log (H-AUTHHTTP-08). On `net/http` there is no shared context at all: `r.WithContext(ctx)` hands a new `*Request` to `next` only, so a logger must wrap *inside* `authnet.Middleware(guard)(logger(mux))` to see the principal — and then it never runs for a 401. On gRPC an interceptor chained after `authgrpc.Unary` gets the authenticated context, and a refusal returns through it. Nothing in any module page, any flow or any example says any of this; `_examples/auth-jwt-gin/main.go` has no logging middleware at all.
**If not ready:** must-hold 3 is a sentence per framework and costs nothing to write down; Gin and gRPC then need nothing else, which is worth saying plainly because the ordinary placement is correct on both. Must-holds 1 and 2 together are unreachable on `authnet` and `authfiber` with any single placement, and the answer is the same one H-AUTHHTTP-08 needs: a library-side log line on the refusal path, so the refused requests appear even though the consumer's middleware never ran. Behind both is a second gap: the library ships no middleware that puts a logger *into* the request context, so [[D-062]]'s `port.Logger(ctx)` seam answers `slog.Default()` for every line this library emits unless the consumer writes a context middleware three times, once per transport shape. `port.WithLogger` exists (`port/log.go:41`) and `grep -rn "WithLogger" --include='*.go' .` finds no non-test caller anywhere in the tree.

### H-AUTHHTTP-12 — Put a CDN in front of the service
**Who:** the author of the same browser-facing API, the week the edge cache goes in
**Wants:** the refusal to be safe behind a shared cache
**Story:** They front the API with a CDN. Nothing breaks at the door — and they have no way to tell from the code that nothing will.
**Must hold:**
1. A response whose body varies by a request header names that header.
**Today:** ❌ missing — and smaller than round 1 claimed
**Evidence:** the refusal body is rendered in the request's language, read from `Accept-Language` (`authhttp.go:49-54`, `authfiber/locale.go:17-23`), and no `Vary` header is emitted anywhere in the tree — `grep -rn '"Vary"' --include='*.go' .` comes back empty. Round 1 sold this on a German caller's 401 being served to an English one, and that does not happen on a stock CDN: 401 is not on RFC 9111's heuristically-cacheable list, and `grep -rni 'cache-control'` over every Go file in the tree returns nothing, so no deployment makes it cacheable by accident. The finding survives as defence in depth rather than as an incident: a `Vary` on a body that varies is free and correct, and a consumer who *does* mark 401s cacheable — a rate-limiting edge rule, a CDN configured to cache all responses — has no way to add it, because a renderer's headers are copied verbatim (`authhttp.go:70-74`) and per H-AUTHHTTP-06 the renderer cannot be passed in.
**If not ready:** one `w.Header().Add("Vary", "Accept-Language")` in `Refuse` and its Fiber twin, unconditional, independent of the mount signature. The dangerous version of this scenario is not the door's: it is an authenticated 200 stored by a shared cache and served to another tenant for want of `Vary: Authorization` or `Cache-Control: private`, and that response is rendered by `porthttp` through the three CRUD bindings. It belongs to the CRUD sweep and is raised here only so it is raised somewhere.

### H-AUTHHTTP-13 — The credential is not in a header
**Who:** the author of a browser app that keeps its session in an HttpOnly cookie, opens a server-sent event stream, and upgrades one route to a WebSocket
**Wants:** the credential read from where the browser actually puts it
**Story:** Their fetch calls could send a header. The event stream cannot, because `EventSource` has no way to set one, and neither does a browser WebSocket upgrade. The token travels as a cookie for one and a query parameter for the other.
**Must hold:**
1. Each binding hands the guard enough to reach a cookie.
2. Each binding hands the guard enough to reach a query parameter.
**Today:** ✅ built for the cookie — `authhttp.Cookie(name)` in `cookie.go`, with the fallback this entry called for · ❌ missing for the query string
**Evidence:** the option's shape is `auth`'s and `Auth.md` H-AUTH-16 owns it. What is this file's is the getter each binding supplies, which is what constrains the shape: `r.Header.Get` (`authnet.go:50`), `c.GetHeader` (`authgin.go:47`), a closure over `c.Get` (`authfiber.go:51`), case-folded metadata (`interceptor.go:115-126`). All four are `func(name string) string` over headers. A cookie is reachable as the raw `Cookie` header a consumer splits themselves; a query parameter is reachable from none of them, because the guard is never handed the request.
**If not ready:** the cookie is a six-line `auth.Lookup`, with one thing to say at the call site that nothing says today: **`auth.Lookup` replaces the credential lookup, it does not add to it** (`auth/guard.go:60-61` assigns `g.lookup`). So a helper written the obvious way turns the `Authorization` header off, in the same story that wants both — the fetch calls send a header and the event stream sends a cookie. It has to fall back to the guard's configured header when there is no cookie, and its doc comment has to say so. The helper belongs *here* and not in `auth`: `authhttp` already imports `net/http`, so `authhttp.Cookie(name) auth.Option` can use `http.ParseCookie` (stdlib since Go 1.23, and `go.mod:3` is `go 1.26`) and hand `auth` back two strings, the three HTTP bindings get it for free, and [[D-055]] stays intact with no RFC 6265 parser to maintain in `auth`. One caveat, which is [[UC-019]] guarantee 15's — *"the same guard object drives all four"* — and which round 1 waved away: a guard carrying a cookie lookup is still a value a consumer can pass to `authgrpc.Unary`, where it reads a metadata key named `Cookie` that no gRPC client sends and refuses every call, silently. The fallback answers that too, and the guarantee otherwise needs narrowing. The query string is the pre-tag decision, and round 1 dropped it (see Contested); the shape is `Auth.md`'s and the per-binding half is this file's: with a namespaced key, `authnet` answers `query:` from `r.URL.Query()`, `authgin` from `c.Query`, `authfiber` from `c.Query`, and `authgrpc` answers `""`. That is four three-line getter changes and no signature change anywhere.

### H-AUTHHTTP-14 — Turn the door on for an API that already has clients
**Who:** the author adopting this library into a service that has been live for two years
**Wants:** a week where a missing or bad credential is recorded and still served, then a switch
**Story:** They cannot redeploy every client on one afternoon. They want the door mounted, refusing nothing, writing down who would have been refused — and then, when the graph flattens, one flag flipped.
**Must hold:**
1. There is a mode where the door authenticates, records the outcome, and refuses nothing.
2. Turning it off is one edit at the mount, not a redeploy with different wiring.
**Today:** ❌ missing — and the option that looks like it is not it
**Evidence:** the four options on the guard are `Header`, `Lookup`, `Optional` and nothing else (`auth/guard.go:46-77`); the three HTTP mounts take `...porthttp.RenderOption` and `authgrpc` takes `Skip`. `Optional()` is the near miss and its own doc comment says why it is not the answer, twice: it does not let a *bad* credential through (`auth/guard.go:66-70`), so a client with an expired token is still refused at the door; and *"an optional guard in front of a gated repository is a 401 at the repository instead of at the door"* (`auth/guard.go:72-75`), so an anonymous request is refused anyway, further in, with a body from a different layer. The only dry run available today is a second deployment with the middleware unmounted, which is a flag day for every client at once.
**If not ready:** the door's half is one transport-neutral option — `auth.Observe()`, which authenticates, stores a principal when it gets one, never returns an error, and writes the outcome through `port.Logger(ctx)` on the same seam H-AUTHHTTP-08 needs. It is additive and it fits [[D-045]] because it decides nothing HTTP-shaped. The honest limit is that the door is only half the phase: a request that failed to authenticate has no principal, and every policy in `crud/decorators/security` refuses an absent one, so a monitor-only door in front of a gated repository still refuses. The repository half is `security`'s and this sweep does not own it. Both halves want the same name and the same week, and neither exists.

### H-AUTHHTTP-15 — The identity is on the connection, not in a header
**Who:** the author of an internal service behind a mesh that terminates mTLS one hop earlier, or one that reads a client certificate itself
**Wants:** an authenticator that derives the principal from the connection
**Story:** They write an `auth.Authenticator` the way the documentation says to, and find it is handed a `Credential` built from a header and a context, and nothing else. There is no certificate to look at and no peer address.
**Must hold:**
1. An authenticator can reach what the connection knows about the caller.
2. Where identity arrives as a header forwarded by a trusted proxy, something warns that a client can send that header too.
**Today:** ❌ missing for 1 on the three HTTP bindings · ✅ on gRPC · ❌ for 2 everywhere
**Evidence:** the three HTTP bindings hand `Guard.Authenticate` a context and a header getter and nothing else (`authnet.go:50`, `authgin.go:47`, `authfiber.go:51`). The context is the *request's*, so an authenticator can read whatever a consumer put in it and can reach neither `r.TLS` nor the peer address — the request never crosses the seam. gRPC is different by accident of shape: the interceptor hands over `ctx` (`interceptor.go:54`, `:73`), which carries `peer.FromContext`, so the same authenticator works there. `authgrpc.md:107-109` tells a gRPC consumer exactly that — *"its principal comes from `credentials.AuthInfo`; write it as an `auth.Authenticator`"* — and it is true on that transport only. [[UC-019]]'s **Out of scope** promises the general case: *"mTLS. A principal derived from a client certificate is an authenticator the application writes; the rest of this use case then applies unchanged."* It does not apply unchanged. For 2, `grep` finds no warning anywhere that a forwarded identity header is indistinguishable from one a client sent.
**If not ready:** on `net/http` the workaround is a shim that copies `r.TLS` into the request context before the door, which works and which nobody writes because nothing says it is needed; on Gin and Fiber the same shim, spelled twice more. The library-side answer is smaller than it looks and is a decision rather than a patch: the bindings could put the connection state into the context they already hand over, under a key `auth` names — which keeps `Guard.Authenticate`'s signature, keeps [[D-055]]'s "no transport type in `auth`" (the key is `auth`'s, the value is the binding's), and gives an authenticator one place to look on all four. Whatever is chosen, [[UC-019]]'s out-of-scope line has to stop saying "unchanged", because today it is only true on gRPC. And one sentence on each binding's page about a trusted-proxy header removes an incident class for the price of a sentence.

### H-AUTHHTTP-16 — Require a permission on a route with no repository behind it
**Who:** the author of the same product API, wiring `/exports`, a webhook receiver and one hand-written admin action
**Wants:** the door's principal checked against a permission before the handler runs
**Story:** They gate their repositories with `security` and it works. Then they write an `/admin/reindex` route with no repository in it and go looking for the middleware that requires a permission. There is not one.
**Must hold:**
1. Requiring a permission on a route is either a shipped thing or a documented recipe.
2. Whichever it is, the refusal is the same envelope as every other failure.
**Today:** ❌ missing for 1 · 🟡 for 2
**Evidence:** the door only ever produces 401. Authorization is [[UC-020]]'s and `crud/decorators/security`'s, and `security` gates repositories: a route with no repository has nothing gating it at all. `auth.Require(ctx)` (`auth/context.go:45-51`) and `auth.HasAll`/`HasAny` are exported and are the whole mechanism, but nothing renders their refusal for a handler — `authhttp.Refuse` exists and is documented for a refusal at the door, not for one a handler decides, and a 403 is a different status than the one it writes. `grep -rn "RequirePermission" auth/` finds nothing; the name lives only in `crud/decorators/security`. `docs/modules/en/authgin.md:41-43` names the gap from the other side — *"Leave this step out and the middleware authenticates and nothing else"* — and the step it names is the repository's.
**If not ready:** the recipe is four lines against `auth.Require` plus the consumer's own 403, per framework, and no page shows it. Whether that stays a recipe is a decision the owner should make before the tag rather than leave as a paragraph aside: every competing middleware ships a `RequirePermission`, and the reason this one does not is that [[UC-020]] put authorization in `security`, where it only reaches repositories. Naming the decision costs a paragraph on `authhttp.md`; reversing it after a tag costs a new package.

### H-AUTHHTTP-17 — Test a service that has the door mounted
**Who:** the same author, the afternoon after mounting it, with a suite that now 401s
**Wants:** a context carrying a principal for a repository test, and a request that gets past the door for a handler test
**Story:** Twenty resources have tests. All of them now run against a gated repository or behind a middleware. They look for the testing helper.
**Must hold:**
1. There is one documented way to build a context that a policy accepts.
2. It is named where somebody writing a test will find it.
**Today:** 🟡 — the mechanism exists and is named in no consumer-facing page
**Evidence:** `auth.WithPrincipal(ctx, p)` is exported (`auth/context.go:22-27`) and is one line. It appears in no `docs/modules/en/auth*.md` "What you get" table, in no usage guide and in no example. The trap it avoids is the one every binding's package doc already warns about for production code: a consumer who guesses `gin.Context.Set` or `fiber.Ctx.Locals` gets a test that compiles, passes and proves nothing (`authfiber.go:11-18`). `crud/crudtest` is this repository's own precedent that a testing story is part of the shipped surface.
**If not ready:** nothing to write — one row in `auth.md`'s table and one paragraph in both usage guides. It is here because "the tests all went red" is a first-afternoon event, and a wrong guess at it is silent.

## The DX this should have

### The call site

```go
// Four steps, and this page is only the fourth. The other three are authjwt's,
// auth's and security's — about sixteen lines in total, per authgin.md:24-58.
r := gin.New()
r.Use(
	cors.New(corsConfig),        // outside the door: a preflight carries no credential
	requestlog.New(),            // outside, and logging after Next, so it sees the principal
	crudgin.Errors(),            // leaves an already-written refusal alone
	authgin.Middleware(guard),   // the door
)
crudgin.New(articles).Mount(r, "/articles")
```

The mount itself is one line and two names, and that is the right length.
Everything above it is what this sweep is about: three of those four entries have
an ordering constraint relative to the door, and none of the three is written
down anywhere in the repository. The `Errors` entry is the one already in
`authgin.md:57`; the other two are what H-AUTHHTTP-02 and H-AUTHHTTP-11 are.

Round 1 called a shorter snippet "byte-for-byte what `authgin.md` shows". It is
not, and the difference is the point: the page builds `authn` and `guard` as two
named steps, mounts `crudgin.Errors()` alongside the door, and its smallest
useful wiring — the one where a row is actually narrowed — is roughly sixteen
lines and a dozen names across `authjwt`, `auth`, `security` and the binding.
That is the newcomer's first afternoon. The mount is not.

### Turning one knob

```go
// Routes that must stay public, under the same global mount. One call per
// mount, from a named slice declared beside the routes; a second call panics.
var public = []authhttp.Route{
	{Method: http.MethodGet, Path: "/healthz"},
	{Method: http.MethodGet, Path: "/metrics"},
	{Method: http.MethodPost, Path: "/login"},
}

r.Use(authgin.New(guard, authgin.Skip(public...)))

// The refusal in the service's own body — the same value crudgin already takes.
// Renderer configuration is the shared porthttp/Port seam used by CRUD and
// gRPC; this binding constructor does not create a fourth renderer dialect.
r.Use(authgin.New(guard))

// A step-up token on the admin subtree. Nothing at this call site says
// "re-authenticate": the default is that a different guard verifies, and only
// the opt-out has a name.
admin := r.Group("/admin", authgin.New(stepUp))

// ...and the opt-out, on the OUTER guard, for the deployment that measured the
// second JWKS lookup and does not want it.
guard := auth.NewGuard(jwks, auth.TrustExistingPrincipal())

// The credential is in a cookie for the event stream and a header for the fetch
// calls. Falling back, not replacing — auth.Lookup replaces, which is the trap.
guard := auth.NewGuard(authn, authhttp.Cookie("session"))

// A week of measuring before the door starts refusing.
guard := auth.NewGuard(authn, auth.Observe())
```

Two of these are on the binding and three are on the guard, and the split is
the design rather than an accident: a route and an `http.Header` are
transport-shaped, and re-authentication, cookies and a dry run are not, so all
four transports get the second group at once. `New` is the second constructor of
H-AUTHHTTP-06's option (c) — the existing `Middleware` keeps its signature and
its call sites, and the knobs get a door that does not cost a v2. The CORS
preflight is deliberately absent from this list: it is answered by ordering a
CORS middleware in front of the door, not by an exemption.

### Why this shape

**The exemption is one type in `authhttp`, not three in three bindings.** Round 1
proposed `authgin.Route` and two identical twins. That is three copies of the
code whose failure mode is an open door, in three modules that cannot import each
other, and it contradicts what each binding's own package doc claims — *"Everything
this package does that is not reading a header and writing a refusal comes from
auth.Guard and authhttp, so the four transports cannot drift apart"*
(`authnet.go:21-23`, `authgin.go:15-17`, `authfiber.go:20-22`). `authhttp` is HTTP
but not framework, which is exactly this. So `authhttp.Route` and one matcher
that normalizes once, and each binding keeps the one line only it can write: Gin
the resolved pattern from `c.FullPath()`, `net/http` and Fiber a cleaned,
unescaped path, because neither of those two can see a resolved route from a
middleware. A consumer with two HTTP shapes in one service then shares one
`public` slice.

**It is spelled `Skip` on all four.** `authgrpc.Skip` already ships and matching a
shipped name costs nothing. The argument types differ — full method names on
gRPC, `authhttp.Route` values on HTTP — and that is a smaller surprise than two
words for one idea.

**One call per mount, and a second one panics.** Accumulation across calls was
round 1's answer to the thirty-route service and it trades away the property the
whole option exists for: an effective set that is the union of every call site is
not a list a reviewer can audit, has no way to see, no way to remove an entry
from, and no error on a duplicate. A named slice splatted once is the same
ergonomics with the audit intact. A duplicate entry and an empty `Path` both
panic at start-up, for the reason [[D-021]] gives — an exemption that silently
matches nothing is a health check that 401s with no diagnostic, and an empty
`Path` on Gin would match every unrouted request at once, since `c.FullPath()` is
`""` for those.

**A path is matched after normalization, and the normalization is a rule rather
than a hazard.** `path.Clean` over the unescaped path, and no match on anything
that still contains an encoded separator. Round 1 argued this from a fail-open
example that was backwards: against an exact list, `/healthz/../admin` and
`//healthz` both fail to equal `/healthz`, so both stay authenticated. The
failure that normalization prevents is the *other* one — a health check that
401s because the probe sent a trailing slash — and the fail-open shape needs a
prefix match, which is what `authgrpc.Skip`'s comment already refuses.

**`authnet`'s package documentation has to move with the option.** It leads with
per-route mounting (`authnet.go:5`), which is the fail-open shape `Skip` exists
to replace. `authnet.Handler` stays for what its own comment says it is for —
one authenticated route among unauthenticated neighbours — and the lead becomes
the global mount plus `Skip`.

**Renderer configuration is not an Authhttp knob.** `New` may carry the
HTTP-only route exemption, but it must consume the same `porthttp.Renderer`
composition chosen by Port and used by Crudhttp and Crudgrpc. A binding-local
`WithRenderer` would make a fourth precedence chain (default versus service hops
versus messages/codes) and re-create the dropped-hop problem those sweeps are
already closing. The release work is one shared renderer contract, then the
Authhttp refusal path reads it; it is not a new per-binding configuration API.

**Required decision gate before `New` or that renderer path is usable.** Amend
[[D-055]]'s forbidden-import bullet to say exactly: *the root `auth` package and
every `auth*` binding must not import `crud`, `crud/decorators/security`, or
`port`; an `auth*` HTTP binding may import `port/porthttp` solely for D-059's
shared HTTP error contract.* The amendment must also state that this exception
does not permit a dependency on a `crud*` sibling or any subsystem. Until that
text is accepted, current `port`/`porthttp` imports are a decision violation,
not precedent authorising `New`, renderer installation, or refusal logging.

**The cost of adding a knob is small and asymmetric.** One nobody turns costs a
paragraph of documentation; one that is missing costs every consumer who needs it
a hand-written binding, and on Fiber there is no helper to hand-write it with.
The asymmetry only holds before the tag.

### What it must not break

- [[D-055]] — **and the shipped code already breaks it, before any of these
  proposals.** Its **What it forbids** says *"Do not import `crud/decorators/security`,
  `crud` or `port` from `auth` or from any `auth*` binding"*, and
  `auth/http/authhttp/authhttp.go:29` imports `port` while `authnet.go:31`,
  `authgin.go:25`, `authfiber.go:30` and `authfiber/locale.go:8` all import
  `port/porthttp`. [[D-059]] moved the renderer seam there deliberately and never
  amended D-055; `docs/modules/en/authhttp.md:7` states `porthttp` as the intended
  dependency. `Auth.md` H-AUTH-17 found the same thing from the logging side.
  Blockers 2 and 6 here both propose putting a `porthttp.Renderer` deeper into the
  bindings. The required D-055 amendment is quoted above: it permits only
  `port/porthttp` from HTTP bindings and only for D-059's error contract, while
  retaining the prohibition on root `port`, `crud`, security, and every `crud*`
  sibling. Until it lands, the shipped imports are evidence of a contradiction,
  not permission to deepen them.
- [[D-055]] again, correctly cited this time — *"do not make an `auth*` module
  require its `crud*` sibling"*. That is the rule H-AUTHHTTP-10's gap breaks, and
  round 1 attributed it to [[FL-019]]'s reading and to [[D-051]]. D-051 says
  nothing about `auth` at all (`grep -n auth docs/ai/decisions/D-051-*.md` is
  empty), and five places in the tree cite it as if it did.
- [[D-045]] — one shared half, one shape every transport can supply. `Skip` is
  HTTP because a route is; renderer configuration remains the shared
  Port/Crudhttp/Crudgrpc seam rather than an Authhttp-local `WithRenderer`;
  `Reauthenticate`, `Cookie`'s option type and `Observe` are on the guard because
  they are not. D-045 also forbids re-deriving a shared rule in a binding, which
  is why the exemption matcher is one function in `authhttp` and not three.
- [[D-021]] — a start-up panic is the acceptable place for magic. Every malformed
  exemption entry fails there, next to `NewGuard(nil)` and `Middleware(nil)`.
- [[D-056]] — no reason in any body. A `Debug` line on the refusal path is
  in-process and does not touch it; an exemption list is about routes, not
  reasons; and `WWW-Authenticate`'s `error=` parameter is the thing D-056 exists
  to keep out, which is why H-AUTHHTTP-07 endorses the omission rather than
  challenging it.
- [[D-062]] — the library never writes to a process-wide logger and no binding
  takes a logging option. The refusal-reason line and `Observe`'s outcome line
  both go through `port.Logger(ctx)` with no new option. A callback-shaped
  `OnRefusal` is the tempting alternative and the one D-062 argues against. The
  residual is H-AUTHHTTP-11's: with no context middleware installing a logger,
  those lines land on `slog.Default()`.
- [[D-016]] and [[D-036]] — nothing proposed here adds a third-party requirement
  anywhere. `authhttp.Cookie` needs `http.ParseCookie`, stdlib since Go 1.23 and
  `go.mod:3` is `go 1.26`; `auth.Reauthenticate` and `auth.Observe` land in `auth`,
  which imports nothing below `errs`. `make check-deps` and `make check-tiers`
  stay green.
- [[UC-019]] guarantee 8 — *"Authentication happens once per request however many
  times the rule is installed"* — is what `auth.Reauthenticate()` challenges, and
  the challenge is that the guarantee is **written too wide**. It was written for
  one guard mounted twice, which is what every test installs; the code applies it
  to any two guards. The wording is as much of the fix as the option is.
- [[UC-019]] guarantee 6 — *"The reason is still recoverable inside the process,
  for a log"* — is claimed as pinned and is unreachable on `authnet` and
  `authfiber` (H-AUTHHTTP-08), and on `authgrpc` only for an interceptor chained
  inside the renderer. Either the guarantee narrows or the bindings close it.
- [[UC-019]] guarantee 15 — *"The same guard object drives all four"* — remains
  true only with the defined fallback: `authhttp.Cookie` first checks the HTTP
  cookie source, rejects a conflict if `Authorization` is also present, and uses
  the configured header only when the cookie is absent. On gRPC the cookie source
  is absent, so that same lookup takes the header fallback rather than trying to
  authenticate an empty `Cookie` metadata value. Without these rules the
  guarantee must narrow.
- [[UC-019]]'s **Out of scope** line on mTLS — *"the rest of this use case then
  applies unchanged"* — is false on the three HTTP bindings and true on gRPC
  (H-AUTHHTTP-15). It has to say which.
- [[UC-019]]'s Out of scope line on session cookies reads across H-AUTHHTTP-13 and
  does not, on this sweep's reading, forbid it: reading a token out of a cookie is
  not session *management*. Confirm it or tighten the line, because the next
  reader will ask.
- `docs/ai/usecases/Index.md:90` records UC-019 as `covered`, and its own Status
  says every guarantee is pinned. Guarantee 8 now has both the same-instance and
  different-instance transport proofs ([[D-076]]); guarantee 6's reporting seam
  remains the disputed half.

## DX verdict
| What the ideal asks for | Today | Distance |
|---|---|---|
| One-line mount | Exactly that on the three HTTP bindings — 1 line, 2 names. The smallest wiring that narrows a row is ~16 lines and ~12 names across four packages (`authgin.md:24-58`); on gRPC the mount alone is two chain statements carrying four constructors, two of them from `crudgrpc` | none on the mount · small on gRPC |
| A live API moved behind the door without a flag day | No monitor-only mode. `Optional()` refuses a bad credential at the door and an anonymous one at the repository, so it is not one. The only dry run is a second deployment with the middleware unmounted | large |
| A browser client that works on the first afternoon | The preflight is 401'd; the fix is one line of middleware ordering and appears in no document | small once known · large while unknown |
| Exempt a named route under a global mount | One option on gRPC; on HTTP, per-route mounting or a group — which costs nothing to write and fails open on the route you forget | small in lines · large in what it risks |
| Public, optional, required and stricter in one service | A stricter nested guard now runs; making a required global mount optional on an inner subtree remains structurally impossible | medium |
| Two credential kinds on one mount | One guard with `Lookup` + `Chain` works and is `Auth.md`'s ✅. Two stacked guards is what consumers reach for, half-works, and is ruled out by no page | small in lines · large in guessability |
| A stricter guard on a subtree | Mount ordinary A then stricter B once each: B always runs. Only adjacent A -> A is idempotent; ambiguous A -> B -> A fails closed because transports cannot infer assurance order | none for A -> B · small composition constraint for re-entry |
| Require a permission on a hand-written route | Four lines against `auth.Require` plus your own 403, per framework, shown nowhere. No `RequirePermission` middleware exists and no page says that is deliberate | medium — and it is a decision, not a patch |
| The refusal in my error vocabulary | Codes and messages already reach it through the render options. A wholesale `Renderer` cannot be passed, though `crudhttp.Renderer` is an alias of the one `Refuse` takes. Workaround: ~12 hand-written lines over exported `Guard.Authenticate` and `authhttp.Refuse` — **on `net/http` and Gin only**, because Fiber's refusal helper is unexported | medium — and pre-tag, unless a second constructor is added |
| `WWW-Authenticate` for a conformance suite | Deliberately absent, argued and control-cased in `authhttp`, mentioned on none of the three binding pages a consumer reads. Stated recourse is a wrapper nobody has written | small, with a documented recipe |
| The reason, server-side | No transport writes a log line. Gin files it with `c.Error` for whoever reads `c.Errors`; gRPC returns it, visible only inside `crudgrpc.Errors`; `net/http` and Fiber drop it | large |
| An outage that looks like an outage | The classification is `Auth.md` blocker 2 and is inherited, not scored here. This file's half: the key fetch runs on the caller's deadline and no binding offers a seam to change that | medium |
| A refused gRPC stream that answers `UNAUTHENTICATED` | `crudgrpc.StreamErrors()` in the chain — correct, and named in no document | small, once you know |
| One access log with the tenant and the 401s | On Gin and gRPC the ordinary placement gives both. On `net/http` and Fiber no single placement gives both, and the fix is a library-side line | small on two · large on two |
| A refusal safe behind a CDN | No `Vary` anywhere. A stock cache will not store a 401, so this is defence in depth rather than an incident | small |
| A credential in a cookie, a query string or a WebSocket upgrade | Cookie: `authhttp.Cookie(name)`, which falls back to the header so the replace-semantics cannot bite. Query string: unreachable from every getter; the shape is a pre-tag decision | small |
| An identity that arrives on the connection | Works on gRPC, where the authenticator gets the ctx. Impossible on the three HTTP bindings without a shim the consumer writes per framework. UC-019 says it applies unchanged | large on HTTP |
| A test that gets past the door | `auth.WithPrincipal` is one line and appears in no module page, guide or example | none in lines · small in guessability |

**Overall:** the part of this module that was designed carefully feels designed
carefully. One line mounts it on three of the four, the transports genuinely
share their decisions rather than four copies of them, the refusal is written
rather than deferred, and the things easiest to get wrong — the Fiber context,
the silence of the body, the optional guard that must still refuse a forgery —
are pinned by tests with controls. Reaching one step further is where it thins,
and in one specific way: the three HTTP bindings take `...porthttp.RenderOption`
and nothing else, so every knob this sweep asks for has no door to come in
through, while `authgrpc` took the extensible shape and got `Skip` for free.
That asymmetry is the whole difference between "add an option later" and "v2" —
and it is dissolvable, by adding a second constructor rather than changing the
first. The escape hatch that makes the rest survivable is real on `net/http` and
Gin and absent on Fiber, which is also the binding with the untested duplicate
refusal, the dropped cause and no example: three findings landing on the same
transport is not a coincidence to leave until after a tag. The proposed HTTP
`New`/renderer route is decision-gated: D-055 must first adopt the narrow D-059
exception above; existing forbidden imports do not make it current behaviour.

## Release blockers found here
| # | What | Severity | Why it blocks |
|---|---|---|---|
| 2 | The three HTTP bindings' mount takes `...porthttp.RenderOption`, which no exemption list and no `Renderer` can pass through (`surface.md:61-62`, `:731`, `:736`); `authgrpc` is already on `Option func(*config)` (`:743-744`) | blocker | **One API decision, two consequences: blockers 5 and 11 both need this door.** Blockers 6, 10 and 12 do not — 10's option is on the guard and 12's fix is one line inside `Refuse`. Changing the four entry points breaks every call site passing render options — and `authnet.Handler`'s trailing variadic cannot be fixed by wrapping. Adding `New(guard, opts ...Option)` beside the frozen `Middleware` breaks nothing and costs one name per binding. Decide before the tag or the choice is a v2. |
| 3 | A CORS preflight is refused by a globally mounted door, and nothing in the repository mentions `OPTIONS`, CORS or preflight | serious | The browser reports a CORS failure that names the wrong subsystem, on day one, for every SPA-backed API. The fix is one line of middleware ordering and it is written nowhere. It is also not fixable by an exemption, which constrains blocker 5. |
| 4 | The refusal reason reaches no log on any transport: dropped on `net/http` and Fiber, filed with `c.Error` on Gin for whoever happens to read it, and on gRPC visible only inside `crudgrpc.Errors` (`authnet.go:52`, `authfiber.go:53`, `crudgrpc/interceptor.go:36`) | serious | A misconfigured rollout produces a reasonless 401 storm with nothing on the server side to diagnose it. [[UC-019]] guarantee 6 claims this is pinned; it is not. `Auth.md` blocker 9 owns the `auth.Reason` half — one incident, two halves, count it once. |
| 5 | No route exemption on the three HTTP bindings while gRPC has `Skip` | serious | Per-route mounting is cheap to write and fails open on the route somebody forgets, which is the failure this module exists to make impossible. Needs blocker 2's door. |
| 6 | The documented gRPC stream wiring omits `crudgrpc.StreamErrors`, so a refused stream answers `Unknown` (`authgrpc.md:57`); `StreamErrors` is in no document, and the stream test asserts the classification rather than the code (`interceptor_test.go:202`) | serious | A fix shipped without its documentation, and the one page showing how to wire a stream shows the shape the fix exists to prevent. It also puts `authgrpc` on the wrong side of [[D-055]]'s "an `auth*` module may not require its `crud*` sibling", which the same page promises at `:11` and breaks at `:79-82`. **Same bug as `Crudgrpc.md` blocker 4; count it once.** |
| 7 | `authfiber` carries a second, complete refusal implementation — its own `locale()` (`locale.go:17-23`) and its own header writer using `c.Set` where `Refuse` uses `Add` (`authfiber.go:66` vs `authhttp.go:72`) — and no test in the tree exercises either | serious | Two happy cases here rest on it: the localized body and every renderer-supplied header. `c.Set` replaces, so a two-value `WWW-Authenticate` or a second `Vary` silently collapses on one binding of three. This is the drift [[D-045]] exists to prevent, arriving inside a single binding, with `grep -rn "Accept-Language" auth/` as the whole proof. |
| 8 | [[D-055]]'s **What it forbids** bans importing `port` from any `auth*` binding, and `authhttp` imports `port` while all four HTTP files import `port/porthttp`; [[D-059]] moved the seam there and never amended it | serious | A decision doc is binding here, and the next agent is told so. Two proposals in this sweep deepen exactly that import. Also five places cite [[D-051]] for the "no `crud*` sibling" rule that lives in D-055 (`authgin.md:11-12`, `:96`, `authfiber.md:11`, `authgrpc.md:11`, `authnet/binding_test.go:19`). `Auth.md` H-AUTH-17 found the same conflict from the logging side. |
| 9 | An `auth.Authenticator` on the three HTTP bindings can reach neither `r.TLS` nor the peer address, while the same authenticator can on gRPC — and [[UC-019]]'s Out of scope promises mTLS "applies unchanged" | serious | The mesh this file's own opening names as one of three ways identity is issued cannot be wired on HTTP without a per-framework shim nobody documents. A use case promising something untrue is worse than one that is silent. |
| 10 | No monitor-only mode: the door either refuses or is not mounted (`auth/guard.go:46-77`), and `Optional()` refuses a bad credential at the door and an anonymous one at the repository (`:66-70`, `:72-75`) | serious | Adoption into a live API is a flag day for every client at once. This is how the library actually gets adopted, and the happy cases all assume a service authenticated from day one. The door's half is one guard option; the repository's half is `security`'s. |
| 11 | No auth binding accepts a wholesale `porthttp.Renderer`, though `crudhttp.Renderer` is a type alias of it and `Refuse` already takes one | sharp edge | Narrow on purpose: `WithCodes` and `WithMessages` already reach the refusal (`porthttp/render.go:51`, `:57`), so only a consumer who replaced the whole envelope — RFC 9457 — gets two shapes depending on whether the door or the repository refused. Rides on blocker 2. |
| 12 | No `Vary: Accept-Language` on a refusal whose body is localized (`authhttp.go:49-54`) | sharp edge | Defence in depth rather than an incident — 401 is not heuristically cacheable and nothing in the tree sets a cache header. One unconditional line in `Refuse` and its Fiber twin, independent of blocker 2. |
| 13 | The mount order relative to the door is different on each framework and documented on none; on `net/http` and Fiber no single placement gives both the principal and the refused requests; no binding installs a request-scoped logger, so [[D-062]]'s seam answers `slog.Default()` | sharp edge | Ops requirement on day one. Gin and gRPC work where loggers already go, which is worth saying; the other two need blocker 4's line before an access log is complete. |
| 14 | Three of the four transports have no runnable example, and the one that exists issues its tokens from `printTokens()` rather than a login route (`_examples/auth-jwt-gin/main.go:161`, `:169`) | sharp edge | Fiber is the binding with the `Locals` trap, the dropped cause, the untested duplicate refusal and no exported `Refuse`, and it has no reference to copy. "The login endpoint cannot require the thing it issues" is named as a first-month problem and demonstrated nowhere. |
| 15 | No test on any transport walks request → middleware → handler → repository → SQL, and the composition claim that `crudgin.Errors`/`crudfiber.Errors` leave an already-written refusal alone is pinned only on `authnet` (`authnet/binding_test.go:31`) | sharp edge | The module's central guarantee is proven in two halves that never meet, and a claim asserted on three stacks is checked on one. `test/go.mod` already carries the replaces and both frameworks, so both tests cost no new dependency. |
| 16 | No require-a-permission middleware and no page saying that is deliberate; the recipe against `auth.Require` is shown nowhere | sharp edge | Every product API has routes with no repository behind them, and today nothing gates one. Whether [[UC-020]]'s split is the final answer is a decision to make before the tag, not a paragraph aside. |
| 17 | A credential in a query string is unreachable from all four getters, and the option's shape must be chosen before the tag | sharp edge | `Auth.md` blocker 11 says the namespaced-key candidate is the only one still free after a tag, because widening `Guard.Authenticate` breaks a documented call. The per-binding half is this file's: four three-line getter changes. Round 1 dropped this and was wrong — see Contested. |
| 18 | `auth.WithPrincipal` is the whole testing story and is named in no module page, guide or example | sharp edge | Every consumer's suite goes red the afternoon they mount the door, and the wrong guess (`c.Set`, `Locals`) compiles and passes. `crud/crudtest` is the precedent that a testing story ships. |

## Contested

- **The preflight is not answered by an exemption.** Reviewers across two rounds
  read H-AUTHHTTP-02 as the argument for making `Skip` method-aware, with
  `Skip("OPTIONS *")` as the shape. Rejected: that is a wildcard, and the one
  property this sweep will not trade is that an exemption list is an exact set a
  reviewer can audit. A preflight is answered by ordering a CORS middleware in
  front of the door, which works today on all three HTTP frameworks. Must-hold 3
  is now written as the refusal rather than as a requirement nobody intends to
  meet — that change is accepted.
- **`authgrpc.Skip` stays the model for the HTTP option, including its name.** A
  reviewer suggested matching a `net/http` ServeMux pattern instead. Rejected:
  `info.FullMethod` is server-chosen and a URL path is client-controlled, so the
  exactness argument does not transplant — but the *word* does, and using it on
  all four is what closes H-AUTHHTTP-03's must-hold 3, which round 1 left failing
  under its own proposal.
- **The query-string helper is back, reversed from round 1.** Round 1 dropped it
  on the grounds that `Guard.Authenticate` is exported and the hand-written
  binding is a dozen lines. Two things break that: the hand-written binding does
  not exist on Fiber, and `Auth.md` H-AUTH-16 proposes a candidate round 1 never
  engaged — a namespaced getter key (`get("query:access_token")`, gRPC answering
  `""`) that does **not** widen [[D-045]]'s getter and does not break the
  documented `guard.Authenticate(r.Context(), r.Header.Get)` call. `Auth.md`
  blocker 11 says the shape expires at the tag. Two release documents giving the
  owner opposite instructions on a decision one of them dates is worse than
  either answer, so this file now carries the per-binding half.
- **The second-guard hole: what is actually disputed is the *default*, not the
  case.** Round 1's Contested entry defended this file's `blocker` against a
  reading of `Auth.md` H-AUTH-05, and that entry is stale — `Auth.md` H-AUTH-12
  now carries the same case, scores it ❌, cites the same `auth/guard.go:91`, and
  says in its own words that it is one blocker with this file's. The live
  disagreement is elsewhere and it is a pre-tag/post-tag one: `Auth.md`'s blocker
  row calls the fix *"additive, and it can land in a patch"*, and this file argues
  the **default** has to change and an opt-in flag does not close the hole,
  because the consumer who is bitten is the one who does not know the failure
  exists. This file's position is kept. The option is additive; the default is a
  behaviour change and cannot land after a tag. One of the two rows has to move
  and the owner decides which.
- **H-AUTHHTTP-01's must-hold 4 stays 🟡, not ✅.** Reviewers in round 1 accepted
  the ✅. The identity reaching the *repository* is the module's stated purpose,
  the binding tests stop at a fake handler, and the integration test bypasses all
  four bindings. Rated as construction, not as a defect — but not as proof.

## Edge cases

### E-AUTHHTTP-01 — A gRPC call carries two different credentials
**Shape:** adversarial input | seam
**Setup:** A client, proxy, or retry layer sends two `authorization` metadata values, one valid for a low-privilege caller and one valid for an administrator.
**What the consumer does:** They expect the door to reject an ambiguous credential, rather than making authorization depend on which intermediary happened to order the values first.
**What must happen:** A binding must refuse multiple values for a single credential name, or document and test a deliberately safe selection rule; silently choosing one is not an authentication decision a service author can audit.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/rpc/authgrpc/interceptor.go:115-125` adapts metadata to the one-string getter by returning `vs[0]`. `auth/rpc/authgrpc/interceptor_test.go:47-55` pins key case-insensitivity, but no test supplies more than one value.
**Blast radius:** silent wrong answer

### E-AUTHHTTP-02 — An optional endpoint receives a syntactically broken credential
**Shape:** adversarial input | boundary
**Setup:** A browser with a stale client sends `Authorization: Bearer `, or a proxy truncates the header to a bare token, to a route mounted with `auth.Optional()`.
**What the consumer does:** They expect a malformed credential that was actually presented to be refused, so the client learns to sign in again instead of quietly receiving the anonymous view.
**What must happen:** Missing and malformed must remain distinguishable at the optional-door boundary; only a genuinely absent credential may proceed anonymously.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/credential.go:56-65` reports both forms as no credential, and `auth/guard.go:95-100` lets any no-credential result through when optional. `auth/credential_test.go:19-21` pins that parser result, while `auth/guard_test.go:59-66` only proves refusal for a syntactically valid token whose authenticator rejects it.
**Blast radius:** confusing error

### E-AUTHHTTP-03 — Credential-source wiring is accidentally empty
**Shape:** misuse | degenerate declaration
**Setup:** A feature-flag branch passes `auth.Lookup(nil)`, or a configuration value becomes `auth.Header("")`; the same guard is optional on a public-and-personalized route.
**What the consumer does:** They expect an invalid credential-source declaration to stop the process before it serves a request.
**What must happen:** `NewGuard` must reject an empty header name and a nil lookup callback. A nil lookup must never silently reinstate `Authorization`, and an empty header must not turn an optional guard into an anonymous pass-through.
**Today:** ✅ handled
**Evidence:** `Header` rejects empty/whitespace names and `Lookup` rejects nil
while `NewGuard` applies their opaque declarations to a private draft. The
finished guard is sealed ready; every transport constructor calls
`Guard.Validate`. `TestAnEmptyCredentialSourceRefusesToStart`, the retained
draft race control, and each binding's nil/zero Guard cases pin declaration
failure before traffic ([[D-076]]).
**Blast radius:** silent wrong answer

### E-AUTHHTTP-04 — An earlier middleware has already put a principal in the context
**Shape:** misuse | seam
**Setup:** A legacy identity shim, or a test helper accidentally left in a production chain, calls exported `auth.WithPrincipal` before a guard that is meant to verify the request.
**What the consumer does:** They expect the guard to verify its own trust boundary, not to treat an arbitrary principal stored by another middleware as proof that this request passed the door.
**What must happen:** Reuse must be limited to an explicitly trusted result of the same authentication boundary, or a consumer must opt in to trusting a pre-existing principal; an unscoped context value cannot silently satisfy a stricter guard.
**Today:** ✅ handled
**Evidence:** A principal without this guard's private marker is not evidence
that the guard ran, so a value installed by legacy middleware cannot bypass it.
`TestADifferentGuardAuthenticatesAgain` and the four transport parity tests pin
that boundary. Only a consecutive repeat whose marker still owns the current
principal state is idempotent; replacing the principal after a marker or
re-entering it after another guard is `ErrAmbiguousGuardOrder` ([[D-076]]).
**Blast radius:** data leak

### E-AUTHHTTP-05 — The gRPC exemption list contains a typo
**Shape:** misuse | degenerate declaration
**Setup:** A service writes `authgrpc.Skip("grpc.health.v1.Health/Check")` without the required leading slash, or accidentally includes an empty entry while assembling its exemption list.
**What the consumer does:** They expect the process to reject a list that is not made of exact gRPC full method names, at the same point it rejects a missing guard.
**What must happen:** `Skip` must validate every non-empty, slash-prefixed full method name at interceptor construction; an audited exact-list API is misleading if a typo is merely stored for later.
**Today:** 🟡 partial
**Evidence:** the `Skip` contract requires a leading slash and exact names in `auth/rpc/authgrpc/interceptor.go:19-27`, but its implementation inserts every supplied string unchanged at `:28-36`. The prefix control in `auth/rpc/authgrpc/interceptor_test.go:171-175` proves exact matching after construction, not declaration validation.
**Blast radius:** confusing error

### E-AUTHHTTP-06 — A public-route helper is given no next handler
**Shape:** misuse
**Setup:** A tired author wires `authnet.Handler(guard, nil)` while registering a one-off protected route.
**What the consumer does:** They expect the wiring error to fail where the route is declared, just as a nil guard does.
**What must happen:** `authnet.Handler` must reject a nil next handler at construction; a valid credential must never be the condition that turns a configuration error into a panic.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/http/authnet/authnet.go:62-63` forwards `next` without validation, while the successful middleware path invokes `next.ServeHTTP` at `:49-56`. The authnet tests contain no `Handler` case (`auth/http/authnet/middleware_test.go:27-133`; `auth/http/authnet/binding_test.go:31-51`).
**Blast radius:** crash

### E-AUTHHTTP-07 — A custom fourth binding has no renderer
**Shape:** misuse | seam
**Setup:** An author of a small custom HTTP binding calls exported `authhttp.Refuse` with an uninitialized `porthttp.Renderer`.
**What the consumer does:** They expect a refusal path to remain fail-closed and diagnosable, not to panic only when the first unauthenticated request arrives.
**What must happen:** The escape hatch needs a constructor-level validation point or a safe renderer fallback; a nil collaborator cannot make the door crash open under load.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/http/authhttp/authhttp.go:67-69` dereferences `rd` immediately. The focused tests pass concrete renderers (`auth/http/authhttp/refuse_test.go:87-97`, `:127-130`, `:155-162`); no nil-renderer case exists.
**Blast radius:** crash

### E-AUTHHTTP-08 — Fiber is asked to emit a repeated response header
**Shape:** seam | adversarial input
**Setup:** A service replaces the refusal renderer and returns two values for `WWW-Authenticate`, `Vary`, or another standards-defined repeated header.
**What the consumer does:** They expect the Fiber door to preserve the renderer's response exactly as the net/http and Gin doors do.
**What must happen:** Every value in the renderer's `http.Header` must reach the Fiber response; the binding must append rather than overwrite.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/http/authfiber/authfiber.go:63-72` loops values but calls `c.Set`, which replaces the previous value. By contrast `auth/http/authhttp/authhttp.go:70-73` uses `Header().Add`, and `auth/http/authhttp/refuse_test.go:85-123` pins two challenges there. Fiber's refusal test checks one JSON body and content type only (`auth/http/authfiber/middleware_test.go:83-98`).
**Blast radius:** silent wrong answer

### E-AUTHHTTP-09 — Fiber's renderer body cannot be encoded
**Shape:** partial failure | seam
**Setup:** A custom renderer accidentally returns a value whose JSON marshal operation fails while Fiber is writing a refusal.
**What the consumer does:** They expect the same safe, bodyless 500 that a net/http or Gin refusal gives, without a framework-specific error body or the original failure reason escaping.
**What must happen:** The binding must marshal before committing the renderer's status, log the encoding failure in process, and return a silent 500 on every HTTP transport.
**Today:** ❓ unverified
**Evidence:** net/http implements the safe fallback at `auth/http/authhttp/authhttp.go:79-90`, pinned by `auth/http/authhttp/refuse_test.go:152-191`. Fiber instead commits the status through `c.Status(status).JSON(body)` at `auth/http/authfiber/authfiber.go:69-72`; no Fiber test exercises an unencodable body, so the final response is not established here.
**Blast radius:** confusing error

### E-AUTHHTTP-10 — The caller cancels while the authenticator is waiting on its provider
**Shape:** partial failure
**Setup:** A client disconnects or its deadline expires while an authenticator is fetching keys or checking an API key store.
**What the consumer does:** They expect the authenticator to receive the inbound cancellation, so it can abandon the provider operation rather than continuing after the caller has gone.
**What must happen:** Each binding must pass its request or stream context unchanged through `Guard.Authenticate` to `Authenticator.Authenticate`, with a binding test for cancellation propagation.
**Today:** 🟡 partial
**Evidence:** authnet, Gin, Fiber, and gRPC pass their inbound contexts to the guard at `auth/http/authnet/authnet.go:49-55`, `auth/http/authgin/authgin.go:46-55`, `auth/http/authfiber/authfiber.go:48-56`, and `auth/rpc/authgrpc/interceptor.go:50-58,69-77`; the guard supplies that context to the authenticator at `auth/guard.go:103-113`. No auth binding test creates a canceled context.
**Blast radius:** confusing error

### E-AUTHHTTP-11 — Two mounts render different service vocabularies at the same time
**Shape:** concurrency | seam
**Setup:** One service mounts an ordinary API with the default renderer and an admin API with a different messages or codes option, and requests to both arrive concurrently.
**What the consumer does:** They expect neither mount's refusal vocabulary or locale to leak into the other mount's responses.
**What must happen:** Option-bearing mounts must have isolated immutable renderer state, while the shared default must derive request-specific locale only from the current context.
**Today:** 🟡 partial
**Evidence:** `auth/http/authhttp/authhttp.go:38-45` shares only the no-option renderer and builds a new renderer for options; `port/porthttp/render.go:123-149` reads the renderer configuration while rendering. `auth/http/authhttp/refuse_test.go:256-266` checks pointer separation for one configured renderer, but no test interleaves requests or distinct middleware mounts.
**Blast radius:** confusing error

### E-AUTHHTTP-12 — An authenticator returns success with no principal
**Shape:** core Auth pointer
**Today:** ✅ handled in `auth.Guard`, before every binding reaches its handler.
**Evidence:** `Guard.Authenticate` converts `(nil, nil)` into `Unauthenticated`
(`auth/guard.go:103-113`) and `auth/guard_test.go:119-129` pins it. This is an
Auth invariant, not a duplicated Authhttp edge finding.

### E-AUTHHTTP-13 — An HTTP request carries two Authorization values
**Shape:** adversarial input | transport ambiguity
**Setup:** A proxy preserves an old `Authorization` value and appends a new one,
or a hostile client sends two different credentials.
**What the consumer does:** They expect the HTTP door to reject the ambiguity,
not to choose the first/last value according to transport implementation detail.
**What must happen:** The shared HTTP credential getter must refuse more than one
value for the configured credential name, matching the gRPC ambiguity policy.
**Today:** ❌ wrong or unhandled
**Evidence:** Authnet gives the guard `r.Header.Get` (`auth/http/authnet/authnet.go:49-55`),
and Gin/Fiber similarly provide a one-string getter (`auth/http/authgin/authgin.go:46-55`,
`auth/http/authfiber/authfiber.go:48-56`). `auth.Guard` accepts that one string
without cardinality information (`auth/guard.go:80-123`). The standard library's
`http.Header.Get` delegates to `textproto.MIMEHeader.Get`, which returns one
header value (`/usr/lib/go/src/net/textproto/header.go:25-28`); no HTTP binding test
supplies repeated Authorization values.
**Blast radius:** silent wrong answer

### E-AUTHHTTP-14 — A configured cookie/query/API-key source conflicts with Authorization
**Shape:** credential-source ambiguity
**Setup:** A browser sends an event-stream cookie or namespaced query API key and
an `Authorization` header with a different credential.
**What the consumer does:** They expect one documented, fail-closed result rather
than whichever custom lookup happens to inspect first.
**What must happen:** A multi-source lookup must reject two present credentials;
fallback to `Authorization` is permitted only when its cookie/query/API-key
source is absent. The same lookup must return the header fallback on gRPC, where
the HTTP-only source is absent, so one guard value remains usable on all four
transports.
**Today:** 🟡 proposed contract
**Evidence:** `auth.Lookup` replaces normal header lookup rather than composing
with it (`auth/guard.go:50-62,120-123`), and the HTTP bindings currently hand it
only header-shaped getters (`authnet.go:50`, `authgin.go:47`, `authfiber.go:51`);
query values therefore cannot reach a guard today. The planned `authhttp.Cookie`
or namespaced query getter must define this precedence/refusal before it expands
the source set.
**Blast radius:** data leak if a lower-privilege source silently wins

## Edge verdict

The worst unclosed edge is credential cardinality and source ambiguity: gRPC
metadata and HTTP headers both collapse repeated `Authorization` values to one,
and cookie/query/API-key support needs an explicit reject-on-conflict rule with
header fallback. Fiber has two independent refusal parity gaps: it demonstrably
loses repeated headers and its encoding-failure outcome is untested, while
net/http has the safe behavior pinned. The remaining request-time panics and
invalid declaration handling are less likely than the security-shaped cases, but
they violate the module's own fail-at-wiring posture. Cancellation reaches the
authenticator by construction; authenticator `(nil, nil)` is a closed core-Auth
invariant rather than an Authhttp release finding. `auth.WithCredential` /
`CredentialFrom` remain Auth proposals pending a D-055 amendment; therefore
cookie/query support here must not imply Remote can forward a credential until
Auth records that approved lifetime and placement contract.

## Release blockers found here (edge)
| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A gRPC credential with repeated metadata values silently authenticates the first value (`auth/rpc/authgrpc/interceptor.go:120-124`) | serious | An application cannot audit which caller wins when a proxy, retry layer, or hostile client supplies two credentials. Reject the ambiguity or make and pin an explicit selection policy before a protocol-facing release. |
| 2 | Fiber overwrites repeated renderer headers instead of preserving them (`auth/http/authfiber/authfiber.go:63-72`) | serious | This is release-blocker 7 above in its concrete edge form; count it once. A custom refusal envelope produces a different protocol response on Fiber than on net/http or Gin. |
| 3 | Empty credential-source declarations are accepted and can silently make an optional guard anonymous (`auth/guard.go:45-62`, `:116-123`) | sharp edge | A declaration must fail before serving. Today its meaning changes at runtime — `Lookup(nil)` restores the default header and `Header("")` supplies no credential — which defeats review of the door's configuration. |
| 4 | `authnet.Handler` and direct `authhttp.Refuse` defer nil-collaborator failures to the first request (`auth/http/authnet/authnet.go:49-63`, `auth/http/authhttp/authhttp.go:67-69`) | sharp edge | Both are crashes on the failure path, rather than a wiring error next to the route or custom binding declaration. |
