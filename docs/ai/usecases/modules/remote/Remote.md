# remote · remote/remotehttp — another service's table, held with the same hands as your own

**Covers:** `github.com/frostgrove/vv/remote`, `github.com/frostgrove/vv/remote/remotehttp`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — `GetAll` can silently truncate, `GetByID` drops a narrowing predicate, and a remote resource has no scope seam; edge cases add credential disclosure through URL userinfo, redirect following, hooks that still run after cancellation, and ambiguous write outcomes after a lost response.

## What a consumer is actually trying to do

The problem that brings someone here is not technical. The rows they need are
behind a team they do not sit with, on a deploy calendar they do not set, in a
repository they may not have read access to. Everything that follows is a
consequence of that: the model is a copy, the query policy is somebody else's
config file, and the failure they will be paged for at 03:07 was introduced by a
merge they never saw.

What they want first is to stop having "the article lives in another service" be
a fact their code knows about. One handle beside the repositories they already
hold, the same method names, the same predicate vocabulary, the same `errors.Is`
branch. If the article moves back into this process next quarter, the diff is at
the wiring.

Then there is a second contract nobody warned them about. The far service
decides what a request is allowed to ask for — which fields are filterable, how
long a list may be, how deep a predicate may nest, whether a whole table may be
asked for at all. That decision lives in another repository, is invisible from
the call site, and turns a call that works against a local repository into a 400
against a remote one. They cannot see it, cannot assert it at wiring, and find
out from a log line that does not name it.

Then the boring middle of the month. The token expires halfway through a job.
The estate is multi-tenant, so a forgotten predicate is somebody else's customer
list. A walk runs at 03:00 and forty thousand corrected rows go back at 04:00.
The struct they copied three months ago has drifted from the one the other team
ships. A config map goes out with the wrong hostname and somebody wants to know
in ten seconds rather than after a week of "that table looks empty".

There is a precondition they should meet on page one rather than on page four:
this only points at a peer that is itself a vv binding. Choosing it over a
hand-written client buys a coupling between two deployables that ship on
different days, and nothing in either process negotiates a version.

And once they have it, they want to serve it again — a gateway, a
backend-for-frontend, a service stitching two others together — without learning
a second shape.

## Happy cases

### H-REMOTE-01 — Read a page of somebody else's table
**Who:** a backend engineer on the billing service who needs the content
service's articles
**Wants:** the drafts, newest first, twenty of them.
**Story:** They add a resource beside their own repositories, hand it a base URL,
and call `Get` with the filter and sort they would have written locally. The page
comes back with the items and the total.
**Must hold:**
1. Wiring is a line, not a package. No generated client, no body structs, no
   status switch.
2. The filter this client sends reaches the far service as the same narrowing a
   local repository would have received — the same rows.
3. The sort, the projection, the preloads, the page number and the limit cross
   too, and a page carries its total.
4. Nothing about SQL leaves the process.
**Today:** ✅ ready — for the translation, which is all these must-holds are
about. Whether the far service *accepts* what crossed is H-REMOTE-19, and it is
the first thing this case's reader hits.
**Evidence:** `remote/resource.go:94-105`; `remote/options.go:57-114` gives every
option one of three answers. `TestAFilterWrittenInGoArrivesAsTheSameNarrowing`
(`remote/roundtrip_test.go:198`) compares the two option lists by *rendering both
as SQL* rather than by diffing documents, which is the only comparison that means
anything when one side holds an opaque predicate. `TestRawSQLIsNeverPutOnTheWire`
(`remote/roundtrip_test.go:424`) is the negative half.
**If not ready:** —

### H-REMOTE-02 — One row by key, and the branch when it is not there
**Who:** the same engineer, rendering an invoice line
**Wants:** `GetByID`, and a 404 of their own when the article is gone.
**Story:** They call `GetByID`, and on the failure they take the branch they
already had. Later they want the article and its recent comments in one call, so
they add a preload with a filter on it. Later still — because the estate is
multi-tenant — they add `crud.Where(crud.Eq("TenantID", …))` to the same call.
**Must hold:**
1. A missing row matches `crud.ErrNotFound`, whichever side of the network it is
   on.
2. A projection crosses on the way to one row.
3. A preload crosses too, including a narrowed one — "the article and its last
   five comments" is the shape everybody asks for.
4. An option that narrows *which row* a single-row read may return either
   narrows it or is refused. It is never dropped.
5. Whatever the far service's own gate decides about a row it hides, the client
   reports faithfully and does not invent a class of its own.
**Today:** ❌ missing — (4) is the one that fails, and it fails silently.
**Evidence:** (1) and (2) hold: `remote/resource.go:130-145`;
`remote/remotehttp/transport.go:247-260` rebuilds the fault through
`port.FaultFrom`, and `port.sentinelFor` (`port/kind.go:286-302`) wraps
`crud.ErrNotFound` for a 404. The single-hop proof of (1) is the control at
`remote/roundtrip_test.go:372-375` — `f.err = crud.ErrNotFound`, then the client's
`GetByID` must still match the sentinel; the `GetByID` subtest at
`remote/roundtrip_test.go:67-87` proves only (2) and the preload path, and asserts
no error at all. The second half of `TestARemoteResourceMountsAsAGateway`
(`remote/roundtrip_test.go:515-518`) proves the sentinel survives two hops.
(3) stops at the transport: a preload carrying a filter or a sort on `GetByID` is
refused with an `*OptionError` (`remote/remotehttp/transport.go:215-240`) because
the entity route spells preloads as a query-string path list and has nowhere to
put a per-relation filter. The same preload is accepted on `Get`
(`remote/options.go:98-110`), and the gRPC client accepts it on `GetByID` because
it sends the whole document — so the same option is honoured at one call site and
refused at another, decided by the transport. It is written down in
`docs/modules/en/remote.md:183-186` and [[FL-013]]; it is not written down
anywhere the caller is looking when they write the call.
**(4) is the find of this round.** `remote/resource.go:139` calls
`port.NarrowForEntity(req)`, which wipes `Filter`, `Terms`, `Search` and `Sort`
(`port/request.go:38-44`) and returns nothing. Locally the same call narrows:
`crud/sqlrepo/repository.go:144-150` appends the caller's options *after* the
primary-key equality, and `crud/repo.go:15-17` says options apply to that read.
So `GetByID(ctx, id, crud.Where(crud.Eq("TenantID", 7)))` answers `crud.ErrNotFound`
locally and another tenant's row remotely, over a 200, with nothing in the error
or the response to say a narrowing went missing. That is the fourth answer
[[D-053]] says does not exist — not refused, not translated, not documented —
inside the module [[D-053]] was written for. The source comment at
`remote/resource.go:136-138` states the misreading in its own words: a filter on
the way to one row means "nothing" and "would only be dropped further along".
It means the caller asked for a row *and* a condition on it.
(5) is not this module's to hold, and this file's first round claimed it as
coverage. Whether a hidden row answers 404 or 403 is the far service's [[D-008]]
decision, and `remote` mirrors whichever status arrived. A far side that answers
403 hands the caller `crud.ErrForbidden`, which does confirm the row exists.
Nothing in `remote/` constructs a policy-hidden row, so no test would fail if
this stopped being true.
**If not ready:** For (3), split the preload today: fetch the row, then `Get` the
children with their own filter. For (4) there is no workaround — the caller
cannot see that the predicate was dropped — so the only safe advice today is
never to pass a narrowing option to a remote `GetByID`, which is advice nobody
has been given. One close serves both: route a `GetByID` that carries a filter or
a narrowed preload through `POST /query` with an equality on the key and
`crud.Limit(1)`, which the far side already serves and which is exactly what
`security.gate.loadScoped` does locally (`crud/decorators/security/security.go:279-302`).
Anything short of that has to be a refusal, because dropping it is the one
outcome [[D-053]] forbids.

### H-REMOTE-03 — Create a row, then patch one field of it
**Who:** an admin-tool author editing a record that lives elsewhere
**Wants:** `Save` for the new one, `Update` with a patch DTO for the edit.
**Story:** They create, and the model comes back refreshed with the key the far
service generated. Later they patch the title and expect the note they did not
touch to still be there. Later still they seed a fixture by setting the key
themselves, the way they do locally.
**Must hold:**
1. An unset key creates; a set key writes that row. The caller does not choose a
   route.
2. The model is refreshed in place with what the service answered, generated
   columns included.
3. A field the patch leaves undefined arrives absent, not null, and does not
   empty a column.
4. A DTO that could not keep that distinction stops the program at start-up, not
   at the first PATCH.
5. Where the write behaves differently because it crossed a network, the caller
   is told at the call site.
**Today:** 🟡 partial
**Evidence:** (2), (3) and (4) hold: `remote/resource.go:173-224`;
`remote/dto.go:29-54` refuses a `crud.Opt` field with no `omitzero`; the
`Save creates` / `Save replaces` / `Update` subtests
(`remote/roundtrip_test.go:100-152`) assert both the refreshed model and that a
field nobody sent arrives undefined;
`TestAPatchDtoThatWouldEmptyAColumnIsRefusedAtStartup`
(`remote/roundtrip_test.go:460`) with two controls. (1) routes correctly and
does not behave the same. `crud.Core.Save` "inserts when the primary key is
unset and upserts otherwise" (`crud/repo.go:22-24`), so locally
`Save(&Article{ID: 42, …})` on a row that is not there inserts row 42. Remotely
a set key is a PUT (`remote/resource.go:189`), and `DefaultService.Replace`
refuses to create when the key is database-generated
(`port/service.go:190-196`, [[D-012]]) — which `crud/meta.go:262-264` makes true
of every int key by default. So the same statement seeds a row locally and
returns `crud.ErrNotFound` remotely, and (5) is missing: neither the method's
doc comment nor `docs/modules/en/remote.md` mentions [[D-012]]. Also unmentioned
anywhere: a read-only consumer still has to name a third type parameter, and
`struct{}` is a legal answer — `checkPatchable` returns nil for a struct with no
`crud.Opt` fields (`remote/dto.go:29-53`), so `remote.New[Article, int64,
struct{}]` compiles and works. The obvious guess is that you must generate and
maintain a patch DTO for a resource you only read.
**If not ready:** For (1) there is nothing to write by hand — the behaviour is
[[D-012]] and it is right; the gap is that `Save`'s doc comment describes the
routing and not the difference. Two sentences in `remote/resource.go:167-172` and
a row in `docs/modules/en/remote.md`'s method table close it, and one line about
`struct{}` closes the other.

### H-REMOTE-04 — Show the far service's validation errors in my own API
**Who:** an API author whose endpoint writes through to another service
**Wants:** the taken email to reach their client as a field error, not as "500".
**Story:** Their handler calls `Save` on the remote resource. The far service
answers 409 with a violation on `email`. They re-render it in their own envelope,
and their client highlights the field.
**Must hold:**
1. The class survives: a collision is a collision, a refusal by policy is a
   refusal by policy.
2. Every violation survives with its path and its machine code.
3. The marker that says the set was cut short survives with them, or a client
   renders a partial list as the whole story.
4. The finer branch survives: a stale write is still a stale write, so the caller
   knows to re-read.
5. Nothing internal arrives — no constraint name, no table, no SQLSTATE, no
   driver sentence — and an internal failure arrives saying nothing at all.
**Today:** 🟡 partial
**Evidence:** (1), (2), (4) and (5) are done and well tested.
`remote/remotehttp/transport.go:247-281` reads `porthttp`'s own tables backwards
rather than keeping a copy ([[D-045]]);
`TestAConflictArrivesAsAConflictWithItsViolations`
(`remote/roundtrip_test.go:232`) asserts the violation *and* sweeps the error
text for five things that must never cross;
`TestAStaleWriteKeepsTheBranchACallerRereadsFrom` (`remote/roundtrip_test.go:287`);
`TestAnInternalFailureArrivesEmpty` (`remote/roundtrip_test.go:302`). (3) crosses
in code and nothing asserts that it does: `grep -rn Partial remote/` returns one
line, the production call at `remote/remotehttp/transport.go:259`. It is one
boolean in a wire struct, which is exactly the field that stops crossing without
anybody noticing, and it is [[UC-017]]'s guarantee riding on this hop.
**If not ready:** One assertion in the existing conflict test — the fake answers
a truncated violation set, the client's fault reports `Partial` true — settles
it.

### H-REMOTE-05 — Point it at the wrong place and find out immediately
**Who:** whoever is on call after a config map went out with the wrong hostname
**Wants:** a loud failure, not an empty table.
**Story:** The base URL now points at an ingress that answers its own 404, or at
a service that never registered the write routes, or at a gateway that answers
`{"message":"ok"}` to everything. They want the call to fail as a call, not to
report that there are no articles.
**Must hold:**
1. A router's or a gateway's own 404 is a failure of the call, never
   `crud.ErrNotFound`.
2. A failure body that is not this library's envelope is never read as one,
   whatever the status said.
3. A base URL that is not a URL is refused where the mistake was made.
4. A 200 that did not come from this library is not read as an answer either.
**Today:** 🟡 partial
**Evidence:** the failure path is airtight —
`remote/remotehttp/transport.go:249-256` and `TestARouters404IsNotAMissingRow`
(`remote/roundtrip_test.go:333`) cover (1) and (2), and the `ProtocolError`
carries a truncated body precisely so a wrong base URL is distinguishable from an
empty table (`remote/transport.go:69-91`). (3) is not there: `Transport` only
trims a trailing slash (`remote/remotehttp/transport.go:40`), so
`content.internal/articles` with no scheme fails on the first call rather than at
wiring, in a library that otherwise panics at start-up for every declaration
mistake ([[D-021]]). (4) is half there. Any 2xx body goes to `json.Unmarshal`
with no content-type check and no strict decode
(`remote/remotehttp/transport.go:156-158`, `remote/resource.go:280-289`). An HTML
interstitial fails to unmarshal and comes back as a plain error naming what was
expected, which is the loud answer. What is silent is a body that is valid JSON
and lacks the keys the call wanted — `{"message":"ok"}`, `{}`, `null`. `decode`
is generic and every method goes through it, so the blast radius is not one empty
page: `Get`/`GetAll` answer an empty page, `GetByID` answers a row that "exists"
with every field zero, `Count` answers 0, `Delete` answers "0 rows went away"
(which `port.DefaultService.Delete` then turns into `crud.ErrNotFound`,
`port/service.go:217-226`), and `Save` writes the zero back over the caller's
model (`remote/resource.go:200`, `*m = saved`). `decode`'s own doc comment names
the requirement it misses: "What it must not do is arrive as a zero value and a
nil error" (`remote/resource.go:279`).
**If not ready:** Nothing to write by hand — the caller cannot see the body. Two
small closes: parse `baseURL` and refuse a missing scheme, and require the
success document to be an object carrying the key the call expects (a page has
`items`) before treating it as an answer. `Transport` returns an interface with
nowhere to put an error, so the refusal follows the precedent two doors down —
`Transport` panics and `TryTransport` does not, the way `remote.New` and
`remote.TryNew` already split (`remote/resource.go:46-60`). The same decode
change closes H-REMOTE-13(3): one edit, one row in the table below.

### H-REMOTE-06 — Carry the caller's identity to the far service
**Who:** anyone whose estate has more than one service in it
**Wants:** the bearer token, the tenant header and the trace id on every outbound
call, per request.
**Story:** A request arrives at their service with a token. They call the content
service on its behalf. Sometimes it is a service account instead, sometimes both
plus an `X-Request-Id` for the trace.
**Must hold:**
1. A header can be set per call, from the calling request's context, not only
   from a value captured at wiring.
2. More than one concern can add a header without the concerns knowing about
   each other.
3. Failing to mint a token fails the call, sends nothing, and is distinguishable
   from the peer being down.
4. There is a worked example, because this is the first thing every consumer
   needs and the easiest to get subtly wrong.
**Today:** 🟡 partial
**Evidence:** (1) works and is not written down: the request is built with
`http.NewRequestWithContext` (`remote/remotehttp/transport.go:124`) and the hook
runs after (`remote/remotehttp/transport.go:132-136`), so `r.Context()` inside
the hook is the caller's context — but the only worked example in the tree
(`docs/modules/en/remote.md:173-179`) closes over a `token` variable, which is
the static case, and nothing shows the one every consumer actually needs.
(2) does **not**: `WithRequestHook` assigns rather than appends
(`remote/remotehttp/transport.go:91`), so a consumer who wires auth and tracing
as two options silently gets only the second — a dropped trace header is
invisible, and a dropped `Authorization` is a 401 nobody can explain from the
client side. (3) is half: the call does fail and nothing is sent
(`remote/remotehttp/transport.go:133-135`), which is the safety half, but the
hook's error is returned raw, so "could not mint a token" and "the peer is down"
are both unrecognised errors that `port.KindOf` reads as internal — see
H-REMOTE-11(4). (4) is missing: no test in this module exercises the hook at all,
and `_examples/` contains nothing about `remote`.
The size of the fix is smaller than this file's first round claimed, and the
first round put it in the wrong package. `auth` defines a `Credential`
(`auth/credential.go:12-20`) and parses one out of the `Authorization` header —
but not in `authhttp`, which never sees a credential and does not import `auth`
at all. The parse is `auth.Guard.credential` (`auth/guard.go:116-124`) and the
discard is the last line of `Guard.Authenticate` (`auth/guard.go:113`,
`return WithPrincipal(ctx, p), nil`). That matters: `Guard` is the
transport-neutral half [[D-055]] and [[D-045]] built so four bindings do not each
hold a copy, so one opt-in there reaches authnet, authgin, authfiber and authgrpc
at once. It is one decision, not four options.
**If not ready:** Today they define a context key, stash the raw token at the
inbound edge themselves, and put every concern into one hook because a second one
would delete the first — about twenty lines across two packages for the first
resource, repeated or hoisted by hand for each further one (H-REMOTE-22). The
close is three parts: append hooks and run them in order; a
`remotehttp.ForwardHeader(names ...string)` reading a documented context value;
and an opt-in in `auth.Guard` that keeps the `auth.Credential` it already parsed
beside the principal. That last one needs an explicit yes or no against
[[D-055]], because a credential travelling the same way as the principal is the
same move. **Scope the middle one in the sentence that introduces it:**
forwarding the caller's own credential is for peers inside the estate. Across a
trust boundary it is credential leakage, and the answer there is a service
account or a token exchange — `remote` is equally happy pointing at a vendor.

### H-REMOTE-07 — Keep tenants apart over a remote resource
**Who:** the author of a multi-tenant SaaS whose orders service reads the
customer service
**Wants:** every call to the remote resource narrowed to the caller's tenant,
declared once.
**Story:** Locally they wrote `Customers.Bind(db, security.Gate(tenantPolicy))`
and every read, write and delete is scoped whether or not the call site
remembered. They expect the same sentence to work over a resource that is not in
this process — and where it cannot, to be told rather than to be given something
that looks like it worked.
**Must hold:**
1. A scope is declared in one place, and every method either applies it or
   refuses. There is no method that quietly does neither.
2. What the scope cannot follow across the network is refused loudly rather than
   applied halfway.
3. A call site does not become the place tenancy is enforced.
**Today:** ❌ missing — and (3) is not closable at this interface by anything a
caller can build.
**Evidence:** `security.Gate` is a `crud.Middleware[M, ID]`
(`crud/decorators/security/security.go:126`), which is
`func(crud.Core) crud.Core` (`crud/repo.go:56-58`), and a `remote.Resource` is
deliberately not a `crud.Core` — the package doc says so in as many words
(`remote/resource.go:16-20`), and what it satisfies is `port.Repository`
(`remote/resource.go:83`), which lacks the `Tx`, `SaveAll`, `UpdateAll`,
`DeleteAll`, `Exists` and `Aggregate` that `crud.Core` declares
(`crud/repo.go:15-53`). Nothing in the repository lifts a `port.Repository` into
a `Core`, and there is no decorator of any kind at the `port.Repository` seam. So
the composition [[D-053]] argues about at length — "stacking that gate over a
remote resource has to fail *here*", and `PrimaryOnly` is accepted specifically
so the gate stays usable — cannot be written at all: it fails at compile time
with a list of missing methods, not with the `*remote.OptionError` the decision
describes. The refusal that decision protects is real and tested
(`TestAnOptionThatCannotCrossIsRefusedBeforeAnythingIsSent`,
`remote/roundtrip_test.go:382`); the composition it protects is unreachable.
And the seam is narrower than it looks. `port.Repository` is eight methods
(`port/repository.go:16-25`), of which `GetByID`, `Get`, `GetAll`, `Update` and
`Count` take `crud.Option`; `Meta`, `Save` and `Delete` take none;
`Resource.Update` refuses every non-nil option it is handed
(`remote/resource.go:212`, `remote/resource.go:292-300`) for the good reason at
`remote/resource.go:205-209`; and `Resource.GetByID` *accepts* a predicate and
throws it away (H-REMOTE-02(4)). So of eight methods, **three** reads can
actually carry a narrowing today, not four.
**If not ready:** Today they either pass `crud.Where(crud.Eq("TenantID", …))` at
every call site — which is worse than a forgetting risk, because on `GetByID` it
is a leak even when nobody forgets — or write their own eight-method wrapper
implementing `port.Repository`. Rejected as a close: doing what
`crudnet.WithScope` does at the binding, which reaches reads only and whose own
doc comment says why that is not the answer (`crud/http/crudnet/options.go:87-96`:
"it looks like protection and is not"). Also rejected: reading the row back
through the scope before each write, which costs a round trip and still leaves a
window nothing can close across a network.
What is left is a read-only scoped view whose writes refuse rather than pass
through, plus the far service's own gate reached through the identity of
H-REMOTE-06 — which makes that case and this one one story. **Say the smaller
promise plainly:** a client-side wrapper stops an honest mistake and nothing
else. The far service has no idea the narrowing was meant to be mandatory, so
must-hold (3) stays open no matter what ships here; a wrapper moves the trusted
call site one layer up inside the same process. The only enforcement is the
owning service's gate. The DX section below spells out what the wrapper can and
cannot promise.

### H-REMOTE-08 — Read more rows than fit in one page
**Who:** whoever owns the reconciliation job, the search indexer or the CSV
export
**Wants:** every article, once a night, and to be sure it was every article.
**Story:** They call `GetAll`, iterate, and write the result somewhere else. The
next morning the numbers do not match and nobody knows why. So they switch to a
sorted walk: a page, then `NextCursor` handed back as `crud.After`, until it runs
out.
**Must hold:**
1. `GetAll` means every matching row, the same as it does locally — or it fails.
   It does not come back short.
2. If the far service will not serve whole tables, the refusal names the fix in
   a place a developer reads, and the job fails rather than half-succeeds.
3. `After` and `Before` cross, and the cursor the far page handed back is the one
   the next call carries.
4. The sort crosses exactly, nulls placement included, because a cursor is the
   sort tuple ([[D-028]]).
5. A walk stops on an empty page and not on `HasNext`, because `HasNext` is the
   field (1) proves can lie.
6. A sort the far side cannot cursor is refused at the first page, with the
   remedy named, rather than silently walked by offset.
**Today:** ❌ missing
**Evidence:** (1) fails under one far-side configuration, and it is not the
exotic one. `GetAll` sets `Unpaged` on the document (`remote/resource.go:117`).
On the far side that becomes `crud.Unpaged()`, and `crud.Options.Resolved` clamps
unpaged down to the repository's `MaxLimit` (`crud/options.go:242-247`) — only
`if maxLimit > 0`, and `MaxLimit` has no default (`crud/sqlrepo/blueprint.go:35`,
`crud/sqlrepo/blueprint.go:53-54`), so on a stock far end `GetAll` really does
return every row. Where an operator declared a page size, the unpaged arm of the
page builder reports `total = offset + len(items)`
(`crud/sqlrepo/repository.go:217-218`), so `HasNext` is computed false
(`crud/page.go:33-39`): the answer says "this is all of it". `GetAll` throws the
page away and returns the items (`remote/resource.go:122-126`), so even that
number never reaches the caller.
The method's own comment is the second half of the problem.
`remote/resource.go:109-111` says "`crud.Options.Resolved` does the same thing
locally, so a repository that caps a page caps this too". That is false, and
[[D-060]] says so in its own text: "The in-process `crud.Repo.GetAll` is
untouched. It has always ignored `MaxLimit` … and still does" (D-060:68-70), and
`crud/sqlrepo/repository.go:270-280` is the branch that bypasses `Resolved`
entirely — pinned by `TestGetAllIsNotCappedByMaxLimit`
(`crud/sqlrepo/paging_edge_test.go:91`). So the one caller who reads the godoc
before writing the job is told the remote behaviour matches a local behaviour
that does not exist. This file's first round cited that comment as evidence
*for* parity; it is a defect in shipped source and gets its own row below.
There is a third ending nobody has put beside the other two: even on a far end
that declared `AllowUnpaged: true` and no `MaxLimit` — the configuration this
file calls stock and correct — a whole table larger than 32 MiB of JSON fails
mid-job at `remote/remotehttp/transport.go:150-155`, as a plain `fmt.Errorf`,
which lands in H-REMOTE-11(4)'s unclassified bucket. `WithMaxResponse`'s doc says
outright there is "no spelling for unbounded"
(`remote/remotehttp/transport.go:94-99`). A team sizing a nightly export needs
that number before they write the job.
(2) half holds: the far end must declare `query.Config{AllowUnpaged: true}` and
the refusal does name the fix (`crud/query/compile.go:387-391`), but the fix
travels in a violation *message* and `Fault.Error()` renders classification only
(`errs/fault.go:68-98`), so a log line reads
`errs: bad_request: bad_query (1 violation)`. That is [[D-047]]'s deliberate
shape and `errs`' to answer — and it is not a one-off attached to this path: it
is how **every** far-side refusal this module surfaces reads in a log, including
H-REMOTE-09's bulk cap and every bound in H-REMOTE-19. See the cross-reference
row.
(3), (4), (5) and (6) are unverified: `After`/`Before` are translated
(`remote/options.go:79-80`), the cursors are plain JSON fields on the decoded
type (`crud/page.go:14-19`), and the far side emits them only when the sort is
unique (`crud/sqlrepo/repository.go:238-254`). Nothing in this module walks one.
The sort translation is asserted for field and direction only
(`remote/roundtrip_test.go:223`); `sortsOf`'s nulls arm
(`remote/options.go:119-135`) has no test on the sending side, and a cursor is
compared against the sort tuple, so a nulls placement that did not cross is a
walk that skips or repeats rows. The receiving side is covered
(`TestSortNullsPlacement`, `crud/query/compile_test.go:149`), which halves the
work.
**A cursorless page is rarer than it looks**, and knowing which cases produce one
is what makes (6) sizeable: `sqlrepo.sortOf` appends a primary-key tiebreaker to
every paged sort by default (`crud/sqlrepo/repository.go:540-556`,
`stableSort: true` at `crud/sqlrepo/blueprint.go:176`), so
`OrderBy(Desc("CreatedAt"))` *does* walk. Three things actually produce a page
with no cursor: the far side declared `UnstablePagination()`, the caller passed
`crud.Unsorted()` (which is `o.NoSort`, and `sortOf` then returns nil), or the
sort hops a relation so the value is not on the row and `setCursors` returns
early (`crud/sqlrepo/repository.go:243-245`). The first two are closed by putting
the key in the `OrderBy`; the third by sorting on a root column.
**If not ready:** Today the honest workaround is not to call `GetAll` at all:
walk pages with `crud.Limit` plus `crud.After` by hand and hope the sort is
unique. Three closes, in the order they pay:
*Client-only, no far-side deploy:* when the far side clamps an unpaged request,
`Options.Resolved` returns `limit = maxLimit`; when it does not, it returns `0`
(`crud/options.go:242-247`), and that number is on the wire as
`PaginatedResponse.Limit` (`crud/page.go:8`, tag `json:"limit"`). `GetAll` is
already decoding and discarding that page. One comparison turns silent truncation
into a refusal, in one module, against a peer running any version — which matters,
because this module's premise is a peer on somebody else's release schedule.
*Far side:* `sqlrepo` must not report a clamped unpaged page as complete.
*Both:* then `GetAll` fails rather than truncating, which is what [[D-060]]
already ruled preferable. A walk that wants many requests asks for them
explicitly — see `port.EachPage` below.

### H-REMOTE-09 — Delete a row, and delete a set of them
**Who:** the author of the account-closure job
**Wants:** one row gone, and later ten thousand rows gone.
**Story:** They call `Delete(ctx, id)` and check the count. Later the retention
job hands `Delete` a slice of keys it built from a query.
**Must hold:**
1. Deleting one row that is not there is distinguishable from deleting one row
   that was.
2. Deleting a set reports how many actually went away.
3. A set larger than the far service will accept is refused by count, not
   truncated, and the caller can find out what the count is.
**Today:** 🟡 partial
**Evidence:** (1) and (2) work and (1) does not work the way the rest of this
document teaches. `remote/resource.go:226-263`: a 404 on the single-row route is
turned back into `(0, nil)` rather than `crud.ErrNotFound`, deliberately and with
a five-line comment saying why — this is a repository, and `port`'s own service
is what turns a zero into `crud.ErrNotFound` (`port/service.go:217-226`), which
it would do twice otherwise. That is right, and it is the opposite of what
H-REMOTE-02 teaches a reader about 404. A consumer who learned "404 means
`ErrNotFound`" writes `if errors.Is(err, crud.ErrNotFound)` after a delete, never
takes the branch, and finds out later. The `Delete` / `Delete many` /
`Delete nothing` subtests (`remote/roundtrip_test.go:153-192`) cover the counts
and the no-round-trip case. (3) is a loud failure rather than a truncation, which
is the right answer, but the client has no part in it: the far side caps a bulk
delete at `port.Rules.BulkCap`, 1024 by default (`port/rules.go:53-66`), and the
client sends the whole key set in one request (`remote/resource.go:254-262`) with
no chunking. **And the number is not readable from the error.** The refusal is
built at `crud/http/crudnet/handler.go:394-395` with
`"at most %d ids per request"`; that text lands in `Message` and in one violation
(`port/sentinel.go:32-37`), and `Fault.Error()` prints classification only
(`errs/fault.go:68-98`). What the job logs is
`errs: bad_request: bad_query (1 violation)`. The number is there, behind an
`errs.AsFault(err)` dig nobody has been told to make.
**If not ready:** Chunk by hand at some number smaller than whatever the far side
declared, discovered by reading its config or by digging into the fault. The gap
is documentation plus the cross-reference row: the `Delete` doc comment explains
the 404 translation to a maintainer and says nothing to a caller, and neither it
nor `docs/modules/en/remote.md` mentions that a large set is one request with a
cap on the other end.

### H-REMOTE-10 — Serve somebody else's resource as my own API
**Who:** the author of a BFF, a gateway, or a public API in front of an internal
one
**Wants:** to mount the remote resource on their own routes.
**Story:** They hold the content service's articles as a repository and mount it
under `/articles` on their own mux. Their clients never learn there are two
services — including when the origin refuses a write and the field names in the
refusal are the origin's vocabulary rather than theirs.
**Must hold:**
1. A remote resource is accepted anywhere a repository is.
2. A filter written by *their* client reaches the origin, not just the gateway.
3. A refusal from the origin reaches their client with its class intact.
4. A violation path from the origin can be re-spelled into the gateway's own
   vocabulary, because the gateway's client never saw the origin's model.
5. The field names the gateway validates its clients against are the origin's
   current ones.
**Today:** 🟡 partial
**Evidence:** (1), (2) and (3) hold: `remote/resource.go:83` asserts the
interface; `TestARemoteResourceMountsAsAGateway` (`remote/roundtrip_test.go:491`)
builds two real servers and asserts the filter **at the origin** rather than on
the response, so one that stopped at the gateway fails. (4) has an answer and
nothing points at it from here: [[D-043]] makes each layer responsible for the
hop it owns, and `port.WithPaths` (`port/service.go:64-66`) is where a gateway
declares its hop. A gateway wired without it ships the origin's model field names
to its own client. This case is where that belongs and it is not mentioned in
`docs/modules/en/remote.md` either. (5) is the join between this case and
H-REMOTE-13 and is nowhere: `Resource.Meta()` is reflection over the **client's
copy** of the struct (`remote/resource.go:85-88`), and a binding mounted over the
resource uses it to resolve field paths, build the projection allow-list and
coerce ids. The morning after the origin ships a rename, the gateway happily
validates its client's filter against a field the origin no longer has, and the
400 arrives from the origin in the origin's vocabulary — which is exactly what
(4) exists to prevent and cannot, because the gateway does not know a rename
happened.
**If not ready:** For (4) the mechanism exists and the pointer does not — one
paragraph in the module doc, one line here, and a gateway fixture that re-spells
a path. For (5) there is nothing to write by hand; it is H-REMOTE-24's coupling
seen from the gateway's side and needs the same answer.

### H-REMOTE-11 — Survive a far service that is slow, huge or gone
**Who:** the engineer whose service must not fall over when a peer does
**Wants:** bounded calls and a failure they can act on.
**Story:** The content service stops answering mid-deploy. Their handler should
give up on its own deadline, and their monitoring should be able to tell "the
peer is down" from "the peer says something is wrong with the request".
**Must hold:**
1. A call is bounded even when nobody set a deadline — including when the
   consumer followed this library's advice and passed their own client.
2. A caller who deliberately budgets *more* gets it. A five-minute export is not
   cut off by a limit it never set.
3. An answer nobody bounded cannot exhaust this process.
4. A peer that is down, timed out or over the cap is distinguishable from a peer
   that answered.
5. The framework does not retry behind their back.
**Today:** 🟡 partial
**Evidence:** (3) is done and tested with a control — a 32 MiB read cap read one
byte past the limit (`remote/remotehttp/transport.go:147-155`,
`TestAnAnswerPastTheCapIsRefusedRatherThanBuffered` at
`remote/remotehttp/transport_test.go:50`, [[D-063]]). (5) is [[D-040]] and holds.
(1) and (2) are two halves of one defect, and this file's first round marked (1)
"done and tested with controls" without noticing the first half. The bound lives
**only** in `defaultClient()` (`remote/remotehttp/transport.go:71`,
`&http.Client{Timeout: DefaultTimeout}`); nothing in `Do` imposes a deadline. So
(1) stops being true the moment a consumer takes this file's own advice and
passes their own client — which this document recommends in three places, and
which the module's own test constructs as a control at
`remote/remotehttp/transport_test.go:37` (`mine := &http.Client{}`, no timeout)
without noticing what it built. And because `net/http` applies `Client.Timeout`
*in addition to* the request context, the shorter wins, so a five-minute context
deadline still ends at 30s — (2) fails in the other direction. Two documents
assert the opposite: `docs/modules/en/remotehttp.md:83-84` ("The caller's own
context deadline still wins") and, worse, a test comment —
`remote/remotehttp/transport_test.go:87-88` says "a caller that bounds the call
itself is not overridden by the client's own backstop", while the body (lines
95-99) only cancels immediately and asserts `context.Canceled`. A test whose
stated purpose is the thing that is false is the vacuous verdict [[D-020]] exists
to refuse, in the specification itself.
(4) is thin: a dead peer, a TLS failure, a timeout, an over-cap answer and a
hook that could not mint a token are all plain `fmt.Errorf` values or raw
returns (`remote/remotehttp/transport.go:133-135`,
`remote/remotehttp/transport.go:138-141`,
`remote/remotehttp/transport.go:150-155`), so `port.KindOf` reads them as
internal and a gateway re-renders 500 for a peer that is merely unavailable.
`errs.KindRetryable` exists and this transport produces it only from a far-side
503 (`port/porthttp/decode.go:30-31`). `errors.Is(err,
context.DeadlineExceeded)` does work, through `*url.Error`.
**If not ready:** Today they pass their own `http.Client` with a longer timeout
to get (2) — and lose (1) doing it — and type-assert `*url.Error` and sniff
`Timeout()` to get (4). One edit closes (1), (2) and H-REMOTE-17: build
`defaultClient()` with an `http.Transport` of its own and **no**
`Client.Timeout`, and impose `DefaultTimeout` with a `context.WithTimeout` in
`Do` only when the caller's context has no deadline — the backstop the doc
already describes, applied to every client rather than to one. The close for (4)
is to classify a transport failure as `errs.KindRetryable` with no violations,
and to wrap a hook's error distinguishably, so a gateway can answer 503 and a
caller can tell "I could not authenticate outbound" from "they are down".

### H-REMOTE-12 — Test my service without the other one running
**Who:** anyone with a CI pipeline
**Wants:** unit tests over code that calls out, with no network.
**Story:** They inject a fake for the resource, or a fake transport under the
real resource, and assert their own logic.
**Must hold:**
1. Faking is cheap — an interface small enough to implement inline.
2. The real encode and decode can be exercised without a database, so what is
   asserted is that the two agree rather than that the client agrees with itself.
**Today:** 🟡 partial
**Evidence:** (1) holds twice over: `remote.Transport` is one method
(`remote/transport.go:65-67`), and `remote.Resource` satisfies `port.Repository`,
so either level can be replaced. (2) is a pattern and not a shipped thing. The
module's own suite does exactly the right shape — a real binding over a fake
repository on an `httptest` server (`remote/fake_test.go:197-207`) — but
`remote/fake_test.go:1` is `package remote_test`, a test binary, so a consumer
cannot import it. Copying it means hand-writing an eight-method `port.Repository`
before the first assertion. `crud/crudtest` does not close this: it is a
statement recorder at the `crud.Source` seam
(`crud/crudtest/recorder.go`), not a repository fake.
**If not ready:** Write the fake. It is mechanical and about 150 lines, and every
consumer writes the same one. An in-memory `port.Repository` beside `crudtest` —
or in `port` itself, since it serves a local repository's tests equally — would
be the shipped version of the pattern the suite already demonstrates.

### H-REMOTE-13 — Hold a copy of somebody else's model, and survive it changing
**Who:** the engineer on the billing service, three months in
**Wants:** the model they declared to keep meaning what the content service
means.
**Story:** They declared `Article` by copying the content team's struct into
their own package. The content team adds `author_id`. Billing loads an article,
changes the title, and calls `Save`. The key is set, so it is a PUT, and the body
carries no `author_id`.
**Must hold:**
1. A field the far service has that this copy does not is not written over by a
   whole-row write.
2. A field this copy has that the far service dropped is not silently read as a
   zero on every row.
3. A far service that renders through a presenter rather than the model does not
   arrive as a model with holes in it.
4. Whichever way the drift is meant to be prevented — a shared package, code
   generation, a hand copy and a contract test — it is written down, because
   every consumer makes this choice on day one.
**Today:** ❌ missing
**Evidence:** Nothing found for any of the four. `Save` with a set key routes to
`MethodReplace` (`remote/resource.go:186-190`), which is `PUT /{id}`, which
`crud/http/crudnet/handler.go:333-335` documents as "the body becomes the whole
row"; `port/service.go:190-212` binds the body into its own `M` and calls
`repo.Save`. So a column the client's copy does not know about is written as its
zero. `remote/dto.go:24-28` reasons explicitly that the model needs no
`omitzero` check because "a replace is a replace" — correct when both sides share
one struct, and the source of (1) when the client's copy is a duplicate another
team can change. In the other direction, the answer is decoded with plain
`json.Unmarshal` and no strict mode (`remote/resource.go:280-289`), so a renamed
or dropped field arrives as a zero value forever; and `crudnet.WithTransform`
(`crud/http/crudnet/options.go:81-85`) is documented as "the place to hide
columns the API should not expose", so a correctly configured, friendly peer
answers 200 with a document that is not `M` — and `Save` then writes that zero
back over the caller's model (`remote/resource.go:200`, `*m = saved`). This is
H-REMOTE-05's class arriving from a peer that is doing everything right, and it
is closed by the same decode change; one edit, one row in the table.
**The presenter half is also the serving team's business.**
`crudnet.WithTransform` carries no warning that turning it on breaks every vv
client holding that resource as a `remote.Resource`, and the serving team will
never read this file. It belongs in that option's doc comment and in
`crudfiber`/`crudgin`/`crudgrpc` beside it, filed the way the cross-reference row
below is filed.
**If not ready:** Today: read the other team's struct, copy it, and find out on
the next deploy. There is no cheap library fix for (1) — a whole-row PUT is a
whole-row PUT — so the close is a decision and a paragraph: say whether the
model is meant to be a shared package, generated from the far service, or copied
with a contract test, and say what each direction of drift does. (2) and (3) have
a mechanical half: decoding an entity with `DisallowUnknownFields` would catch a
presenter and a rename at the first call rather than never, and is a real
behaviour change worth arguing about before a tag rather than after.

### H-REMOTE-14 — Push forty thousand corrected rows into the far service
**Who:** whoever owns the nightly import, the backfill or the reconciliation
**Wants:** to write a set, the way they can already delete a set.
**Story:** The job computes corrections locally and has to land them in the
content service. It writes them one at a time, because that is the only shape
there is.
**Must hold:**
1. Writing many rows costs something less than one round trip per row, or the
   fact that it does not is stated before the job is written.
2. A run that stops halfway leaves the caller able to say where.
**Today:** ❌ missing — and possibly correctly so
**Evidence:** There is no batch write of any kind. `port.Repository` declares no
`SaveAll` and no `UpdateAll` (`port/repository.go:16-25`); `remote.Method` has no
create-many (`remote/transport.go:18-27`); bulk delete is the only set operation
any binding registers (`docs/modules/en/remote.md:53-60`). That is a defensible
design — `crud.Core.SaveAll` exists locally and depends on a transaction, which
is exactly what [[UC-018]] puts out of scope — but it is nowhere stated, and
"there is no `SaveAll`" is what an import job discovers after it has been
written. With `DefaultTimeout` at 30s per call it is forty thousand sequential
round trips with no partial-failure story.
**If not ready:** They write the loop, their own concurrency limit, their own
resume marker. The close may well be one paragraph rather than an API: say that
a remote resource writes one row at a time, say why (a batch write with no
transaction has a partial-failure contract nobody has designed here), and point
at the shape a consumer should use instead. If it is an API, it needs the
partial-failure answer first — which row failed, and what happened to the rest —
and that is a bigger question than a tag.

### H-REMOTE-15 — A credential that expires in the middle of a job
**Who:** the author of the 03:00 export
**Wants:** to notice that the token died, mint a new one, and carry on.
**Story:** The job mints a token at 03:00. It lives an hour. At 04:12 the walk is
still running and every remaining call comes back 401.
**Must hold:**
1. "Your credential is no longer good" is a branch the caller can take, in the
   same idiom as every other branch this module sells.
2. Re-minting and re-calling is the consumer's, and it is written down that it
   is.
**Today:** 🟡 partial
**Evidence:** The classification is right and the branch is a different shape
from every other one. A 401 is `errs.KindUnauthorized`
(`port/porthttp/decode.go:26-27`), and `port.sentinelFor`
(`port/kind.go:286-302`) has arms for `NotFound`, `Forbidden`, `Conflict` and
`BadRequest` and returns nil otherwise — so the fault wraps nothing and
`errors.Is` finds nothing to match. The caller has to reach for
`errs.AsFault(err).Kind`, which works, and which is not the idiom H-REMOTE-02 and
H-REMOTE-04 teach. [[D-040]] means the framework will not re-mint or retry, which
is right, and the consuming half of that is documented nowhere.
**If not ready:** `errs.AsFault(err)` and a kind comparison, written once and
copied. The cheap-looking close is not cheap and this file's first round said it
was: `sentinelFor` is in package `port`, and `port` is on the contract manifest
(`Makefile:37`, `TIER0 := crud crud/crudtest crud/query errs errs/sqlerr port
port/porthttp`, held by `make check-tiers`), so it cannot return
`auth.ErrUnauthenticated` (`auth/errors.go:17`) — `auth` is not on the manifest
and the attempt fails the check. Giving the 401 a sentinel therefore means a
**new exported name in `crud` or `errs`**, in a contract package, immediately
before a tag. That is a decision for the owner, filed as a cross-reference row
below, not a line to write here. The alternative that costs nothing is one
paragraph in the module doc: the auth branch is the one that goes through
`AsFault`, and why. Silence is the option that leaves a consumer writing
`errors.Is` against something that will never match.

### H-REMOTE-16 — Read back what you just wrote
**Who:** the admin tool that POSTs an article and immediately renders the
confirmation page
**Wants:** the row it just created.
**Story:** They `Save`, then `GetByID` with the key that came back. The content
service reads from a replica. The row is not there yet.
**Must hold:**
1. A read that has to see a write can say so.
2. Where it cannot, the caller learns that before they design the flow, not from
   an intermittent 404 in production.
**Today:** ✅ ready — as a documented limit rather than as a capability
**Evidence:** (1) is impossible over a transport and that is decided, not missed:
`crud.PrimaryOnly` is accepted and travels as nothing
(`remote/options.go:42-50`, [[D-053]]), because refusing it would break the
security gate's own composition — the gate sets it on nearly every call.
(2) is where this module is at its best, and this file's first round did not say
so. `docs/modules/en/remote.md:134-141` puts it under a heading that states the
reasoning — "Accepted, and cannot be honoured — named here because a silently
dropped option is the one failure a caller cannot see" — and names the
consequence in the next sentence: where a replica lags, the far service is what
has to be configured. [[UC-018]]'s Status section says the same. That is the
standard the rest of this document is holding everything else to, met. What it
buys the consumer is a design-time fact rather than a runtime one: the far
service's replica policy is now this service's correctness problem, and the team
that owns it is a different team.
**If not ready:** The story as told does not need a read-your-writes read at all,
and the one-line answer belongs here rather than in an em dash: `Save` refreshes
`*m` in place with what the service answered (`remote/resource.go:196-201`,
H-REMOTE-03(2)), so render the confirmation from the model you already hold. The
second call is the bug. Where a *later* request has to see the write — a redirect,
a second service — the replica policy is the far service's and there is nothing
this side can do about it.

### H-REMOTE-17 — The peer is healthy and this process is the bottleneck
**Who:** the engineer whose service holds a remote resource on a request path
**Wants:** three hundred requests a second without a handshake on each one.
**Story:** p99 goes to 400ms. Neither service's logs show anything wrong. The far
service reports single-digit milliseconds.
**Must hold:**
1. A remote resource on a hot path reuses connections by default, or the fact
   that it does not is stated where a consumer wires it.
**Today:** ❌ missing
**Evidence:** `defaultClient()` is `&http.Client{Timeout: DefaultTimeout}` with
no `Transport` field (`remote/remotehttp/transport.go:71`), so every call goes
through `http.DefaultTransport`, whose `MaxIdleConnsPerHost` is 2. A hot remote
resource spends its time in TCP and TLS handshakes. `WithClient` is the fix and
`docs/modules/en/remotehttp.md:35` mentions "connection limits" in passing;
nothing says a consumer will need it. Every other case in this file treats the
network as something that fails; this is the one where it works and is slow, and
it is the most common first-quarter production surprise of the set. It is also
the same three lines as H-REMOTE-11(1) and (2): one constructor, three defects,
one edit.
**If not ready:** They pass a tuned `http.Client`, once they have found out —
and by doing so remove the only deadline in the module, which is why the two
cases close together. The close is `defaultClient()` built with an
`http.Transport` of its own plus the context backstop in `Do`, and a paragraph in
`docs/modules/en/remotehttp.md` beside the timeout one. There is a cost worth
naming: a client with a transport of its own no longer shares the process's idle
pool, and for a service that calls one peer that is the point.

### H-REMOTE-18 — Retry a failed write without creating the row twice
**Who:** the engineer who added a retrying round tripper because the module doc
told them to
**Wants:** a POST that failed on a connection reset to be safe to send again.
**Story:** They wire retries into their `http.Client`, because [[D-040]] says the
framework will not do it and `WithClient` is where a policy belongs. A create
gets a 502 from a proxy after the origin already wrote the row. The retry writes
it again.
**Must hold:**
1. A write that can be retried can be made idempotent, or the advice to retry is
   qualified.
2. A hook can tell which call it is decorating, and which resource, so a key can
   be attached to a create and not to a list.
**Today:** ❌ missing
**Evidence:** `MethodCreate` is a plain `POST` with no idempotency header
(`remote/remotehttp/transport.go:181-184`). `WithRequestHook` takes
`func(*http.Request) error` (`remote/remotehttp/transport.go:90-92`), so a hook
cannot tell a create from a list without re-parsing the method and the path, and
the `remote.Call` that carries `Method`, `ID` and `Body` is in hand one frame up
(`remote/remotehttp/transport.go:117`). Nor can it tell *which resource* it is
on: a process holding eight of the peer's resources holds eight transports
(H-REMOTE-22), and nothing in the hook's arguments distinguishes them. This
file's first round recommended retries in the caller's client under "What it must
not break" and did not mention any of it, which is worse than mentioning
neither: a reader who follows it ships duplicate rows.
**If not ready:** Retry reads only, and say so. The close is one signature, and
this file picks it rather than offering two: **widen `WithRequestHook` in place**
to `func(*http.Request, remote.CallInfo) error` and make it append rather than
assign. One name, breaking now and free before a tag, a deprecation after it —
and it closes H-REMOTE-06(2) at the same time, which is why the two are one row
below. `CallInfo` and not `Call`: a hook has no business reaching through `Body`
or `IDs`, which are slices it can write through. That does not make the far
service idempotent — nothing here can — but it puts the key where a service that
implements one can read it.

### H-REMOTE-19 — My filter works locally and 400s over the wire
**Who:** the engineer who wrote the call on Tuesday and deployed it on Thursday
**Wants:** the narrowing they wrote to be a narrowing the far service accepts.
**Story:** They filter on a field the far service did not put in `Filterable`.
Or they pass 4,000 ids to `crud.In`. Or their predicate nests six deep. Or they
preload seventeen relations. Every one of those works against a local
repository, compiles, passes review, and answers 400 in staging.
**Must hold:**
1. What the far service will accept is discoverable before the call is written,
   or at worst is named in the refusal in a place the caller reads.
2. A caller who knows the peer's policy can assert it at wiring rather than
   trusting a comment.
3. The bounds that exist are visible from this side at all.
**Today:** ❌ missing
**Evidence:** The far side's `query.Config` (`crud/query/compile.go:29-82`) is a
second contract this module's caller cannot see. Ten fields decide it:
`MaxDepth` (default 6), `MaxConditions` (64), `MaxPreloads` (16), `MaxInValues`
(1024), `MaxSort` (16), `AllowUnpaged` (false), and the five allow-lists
`Filterable`, `Sortable`, `Selectable`, `Preloadable`, `Searchable` — plus
`sqlrepo.MaxLimit` and `port.Rules.BulkCap` beside it. `remote` names exactly one
of them anywhere in its docs (`AllowUnpaged`, via `GetAll`). There is no
constructor option that takes a peer's `query.Config`, nothing that fetches one,
nothing that validates a request against one before sending. `grep -rn
"query.Config" remote/` returns nothing.
And the refusal that names the fix does not render: the compiler's messages are
good — `crud/query/compile.go:375-377` tells a caller their cursor sorts on a
column the endpoint does not expose to filtering, and `:542-543` gives the
actual cap and the actual count — and every one of them
travels in a violation message that `Fault.Error()` never prints
(`errs/fault.go:68-98`). The caller's log says
`errs: bad_request: bad_query (1 violation)`, which is the same line H-REMOTE-08
and H-REMOTE-09 produce for entirely different mistakes.
**If not ready:** Today: read the other team's `Define` call, or send the request
and dig the message out with `errs.AsFault(err)`. Two closes of different sizes.
The cheap one is a paragraph: this module's headline promise — the same
narrowing, the same rows — holds only inside the far service's query policy, and
here are the ten fields that policy has. The real one is a way to say at wiring
what the peer accepts, so a `Filterable` violation is a start-up failure or a
local refusal rather than a staging 400 — which needs the two teams to share
something, and is therefore the same conversation as H-REMOTE-24.

### H-REMOTE-20 — Fetch fifty rows by key without fifty calls
**Who:** the author of the BFF rendering fifty invoices, and of the job holding
five thousand keys
**Wants:** the rows for a set of ids, in one request.
**Story:** They have fifty article ids from their own tables. They reach for
`GetByID` in a loop, because that is the method whose name matches what they
want. Fifty sequential calls at up to 30s each.
**Must hold:**
1. There is a shape for "these keys, in one request", and the consumer is told
   what it is.
2. The consumer is told what bounds it, before the job that exceeds them is
   written.
**Today:** 🟡 partial — it works and nothing says so
**Evidence:** There is no `GetMany`, and there does not need to be: the shape is
`Get(ctx, crud.Where(crud.In("ID", ids)))`, and `crud.In` marshals and crosses
like any other predicate (`remote/options.go:90-96`). Nothing in
`docs/modules/en/remote.md` or in the method table says so, and the method a
consumer reaches for by name is the one that costs fifty round trips. Three
limits bound it and none is mentioned together with it: the far side's
`MaxInValues` (1024 by default, `crud/query/compile.go:91`), its `MaxLimit`, and
its default page size (`DefaultLimit`, 20) — so a caller who chunks the ids at
1000 and forgets `crud.Limit` gets twenty rows back and no error. This is the
mirror of H-REMOTE-14, and the far more common half: the file had a case for
batch writes and none for batch reads.
**If not ready:** Nothing to write by hand, which is what makes this cheap. Three
sentences in the module doc: the shape, the two caps that bound it, and "chunk at
something below the peer's `MaxInValues`, and pass a limit". A `port.EachPage`
walk (below) over the same predicate covers the five-thousand-key case.

### H-REMOTE-21 — The peer throttles me
**Who:** the same export job, at 03:07, behind the estate's gateway
**Wants:** to back off for as long as the peer asked, and not to page anyone.
**Story:** The content service — or the gateway in front of it — starts answering
429 with a `Retry-After`. The job's error handling sees the same thing it sees
when the peer is down.
**Must hold:**
1. "Slow down" is distinguishable from "broken".
2. The interval the peer named reaches the caller.
**Today:** ❌ missing
**Evidence:** (1): `porthttp.KindForStatus` (`port/porthttp/decode.go:22-41`)
maps 503 to `errs.KindRetryable`, 401/403/404/409/422/400/413 to their kinds, and
everything else — 429 included — to `errs.KindInternal`. So a throttled caller
and a broken peer produce the same class, and a gateway holding the resource
re-renders 500 for both. (2): `remotehttp` never reads `resp.Header` at all —
`grep -n "resp.Header" remote/remotehttp/transport.go` is empty — so the
`Retry-After` that **vv's own renderer** writes on a 503
(`port/porthttp/render.go:146-148`) is discarded by vv's own client. Two halves
of one framework, disagreeing. H-REMOTE-11 covers down, slow and gone; throttled
is the fourth state, and it is the one an H-REMOTE-08 export actually causes.
**If not ready:** Today: type-assert nothing, because there is nothing to assert
on — the caller sees an unclassified error and either retries blind or gives up.
Two rows in one table and one field: add 429 to `KindForStatus` as
`KindRetryable` (it is the same class the forward table already gives 503), and
carry `Retry-After` onto the fault the client rebuilds so a caller can honour it.
The forward table is `porthttp`'s, so the first half is a cross-reference; the
second is this module's.

### H-REMOTE-22 — Eight of the peer's resources in one process
**Who:** the engineer wiring the billing service against the content service
**Wants:** to hold articles, authors, categories, media, comments, tags,
revisions and collections without repeating the wiring eight times.
**Story:** They write the first resource from the module doc. It works. They
write seven more by copy and paste. On the sixth, the `WithClient` line does not
make it across; on the seventh, the auth hook does not.
**Must hold:**
1. The client, the hooks and the caps are declared once per peer, not once per
   resource.
2. A resource wired without them fails visibly rather than quietly.
**Today:** ❌ missing
**Evidence:** `baseURL` is the resource's mount prefix
(`docs/modules/en/remotehttp.md:30-31`), and `remotehttp.Transport(baseURL,
opts...)` (`remote/remotehttp/transport.go:39`) is the only constructor. There is
no per-peer object: eight resources are eight transports, each repeating the
host, the pooled client, the hook chain and the response cap, or hoisting them
into a `[]remotehttp.TransportOption` variable nobody has written down anywhere.
Both failure modes are silent — the resource with the default client gets
H-REMOTE-17's latency on that one resource only; the one missing the hook gets
401s from that one resource only — and neither shows up in a review of a diff
that already had seven correct lines above it. This is also where H-REMOTE-06 and
H-REMOTE-17 stop being separate problems: both are fixed by a shared, tuned,
hooked client that eight constructors have to be handed by hand.
**If not ready:** Hoist the option slice and hope. The cheap close is the idiom
written down in `docs/modules/en/remotehttp.md`, with the shared slice named. The
real close is a peer-level constructor that mints resources under one client, one
hook chain and one cap — which is also where H-REMOTE-18's `CallInfo.Resource`
label gets its value, so one shared hook can branch per resource instead of
re-parsing a path.

### H-REMOTE-23 — A local write and a remote write in one request
**Who:** the engineer on the orders service, in their first month
**Wants:** the order saved here and the line created there, or neither.
**Story:** The handler opens a transaction, saves the order, calls the content
service to create the line, and commits. The remote call fails after the local
write; or it succeeds and the local transaction rolls back.
**Must hold:**
1. There is a stated position on ordering and on the orphan, because every
   consumer of this module hits this.
2. Whatever the position is, the compensating action it implies is safe to
   perform.
**Today:** ❌ missing — and correctly out of scope as an API, which is not the
same as answered
**Evidence:** `Resource` is deliberately not a `crud.Core` and has no `Tx`
(`remote/resource.go:16-20`), and [[UC-018]] puts transactions out of scope. Both
settle the API question and neither answers the job. Nothing in `remote/`,
`docs/modules/en/remote.md` or [[UC-018]] says which order to write in, what to
do with the orphan, or that the answer depends on something else in this file:
a compensating retry of the remote create is only safe if the create is
idempotent, and H-REMOTE-18 says it is not. So the two open questions are
coupled, and neither is stated where the consumer designing the flow will see
them.
**If not ready:** Today they guess. The close is a paragraph, not an API: write
remotely first and locally second when the remote write is the one that cannot be
undone, keep the local transaction as short as the remote call is not, and
reconcile rather than compensate until a create can carry an idempotency key. A
sweep whose job is to find what the code cannot do should say this out loud
rather than leave it in the out-of-scope list of the document it is auditing.

### H-REMOTE-24 — Deploy on a different day than the peer
**Who:** the platform engineer who has to approve this dependency
**Wants:** to know what breaks when one of the two services ships first.
**Story:** The content service ships on Tuesday, billing on Thursday. Between
them, the wire DSL, the error envelope, the route set and the model are shared
between two binaries built from two commits.
**Must hold:**
1. The precondition is stated where a reader meets it: this talks to a peer that
   is itself a vv binding.
2. What is a compatibility surface between the two deployables is written down,
   and what happens when they differ is stated.
**Today:** ❌ missing
**Evidence:** (1) is implied by everything and stated nowhere in the consumer's
terms — a reader whose peer is a third-party REST API can get several sections
into `docs/modules/en/remote.md` before finding out this is not for them.
(2): there is no version negotiation of any kind. `grep -rni "version" remote/`
returns exactly one hit and it is `CodeStaleVersion`
(`remote/remotehttp/transport.go:267`), which is about a row's optimistic-lock
column, not about the two binaries. No header is sent, none is read
(`resp.Header` is never touched), and nothing compares the two sides' idea of the
document. So the wire DSL (`crud/query`), the envelope (`port/porthttp`), the
route set (`remote/remotehttp/transport.go:164-200`) and the model become a
compatibility surface the moment the tag lands, and a consumer choosing this over
a hand-written client is buying a coupling between two deployables that the
module's own thesis — "what the network costs is written down rather than
discovered" — never prices.
**If not ready:** Nothing to write by hand. Two paragraphs and one decision. The
paragraphs: name the precondition in the first screen of
`docs/modules/en/remote.md`, and list what the two sides share. The decision is
the one that gets harder after a tag rather than before: whether the wire DSL and
the envelope are versioned at all, and if not, what the compatibility promise is
between a client and a service on different `vv` releases. H-REMOTE-10(5) and
H-REMOTE-19 are the same question arriving from two other directions.

### H-REMOTE-25 — Find the slow call, and find it in the peer's logs
**Who:** the engineer holding the pager during the incident
**Wants:** to know which remote call was slow, on which resource, and to join it
to the peer's own log line.
**Story:** Latency is up. They want a metric per resource and per method, and a
request id that both services printed.
**Must hold:**
1. Outbound calls can be measured and labelled without re-parsing a URL.
2. A caller's log line and the peer's log line for the same call can be joined.
**Today:** ❌ missing
**Evidence:** (1): `WithClient` takes an instrumented round tripper and
`WithRequestHook` runs before every send — and both see only an `*http.Request`
(`remote/remotehttp/transport.go:90-92`, `:124-136`). A metric can therefore be
labelled only by re-parsing method and path, and with several resources in one
process (H-REMOTE-22) nothing says which resource it was. The `remote.Call` that
knows is one frame up (`remote/remotehttp/transport.go:117-118`) and is not
passed down. (2): no response header is ever read, so the peer's own request id
never reaches the caller's log line; the outbound id would have to be minted in
the hook and correlated by hand. `port.Logger(ctx)` (`port/log.go:26`) is the
library's logging seam and nothing in `remote/` uses it, which is correct under
[[D-062]] — the library should not decide to log — but then the file owes the
consumer a sentence about where their outbound call telemetry comes from, and it
did not have one.
**If not ready:** Today: an instrumented round tripper that re-parses the path,
and a hand-minted correlation id. The `CallInfo` from H-REMOTE-18 closes (1) for
free once it carries a resource name, which is the same name H-REMOTE-22's
peer-level constructor would supply. (2) needs `resp.Header` to be read at all —
the same one-line gap H-REMOTE-21 needs for `Retry-After`.

## The DX this should have

### The call site

```go
// Two statements. No generated client, no body structs, no status switch.
articles := remote.New[Article, int64, ArticleUpdate](
    remotehttp.Transport("https://content.internal/articles"))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Status", "draft")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Limit(20))
```

Five lines, four concepts: three type parameters, a transport, a base URL that
must be the resource's mount prefix, and the `crud.Option` vocabulary the caller
already knows. For a consumer that only reads, the third type parameter is
`struct{}` and there is no DTO to generate. That much exists today and is worth
protecting exactly as it is.

### Turning one knob

```go
// Once per peer, not once per resource. Eight resources share one client, one
// hook chain and one cap, and none of them can be wired without them.
content := remotehttp.Peer("https://content.internal",
    remotehttp.WithClient(pooled),        // connections, timeouts, instrumentation
    remotehttp.WithRequestHook(forwardToken),
    remotehttp.WithRequestHook(forwardTrace))   // appended, not assigned

articles  := remote.New[Article, int64, ArticleUpdate](content.Resource("articles"))
customers := remote.New[Customer, int64, CustomerUpdate](content.Resource("customers"))

// The hook in full, because "forward the Authorization header" is the thing
// every consumer writes first and the thing no doc in the tree shows.
// CallInfo carries Method, ID and Resource — enough to label a metric or scope
// an idempotency key, and no Body or IDs a hook could write through.
func forwardToken(r *http.Request, _ remote.CallInfo) error {
    c, ok := auth.CredentialFrom(r.Context()) // PROPOSED ONLY: conditional on an accepted D-055 amendment
    if !ok {
        return remote.HookError("no credential to forward to the content service")
    }
    r.Header.Set("Authorization", c.Scheme+" "+c.Token)
    return nil
}

// A scope declared once. Reads carry it; writes refuse with a class a gateway
// can render. Writes that must happen keep the plain handle, and what protects
// them is the far service's own gate reached through the token above.
customersRO := port.ScopedReads(customers,
    func(ctx context.Context) (crud.Predicate, error) {
        return crud.Eq("TenantID", tenant.From(ctx)), nil
    },
    port.RelationsScopedRemotely()) // "the far service scopes its own relations"

// A walk that cannot silently stop early, and refuses a sort it cannot walk.
// A free function, so it composes with the wrapper above instead of being a
// method the wrapper would have to forward.
err := port.EachPage(ctx, customersRO,
    []crud.Option{crud.Limit(500), crud.OrderBy(crud.Asc("ID"))},
    func(p crud.PaginatedResponse[Customer]) error { … })
```

Be honest about the size: the short path is 5 lines and the block above is about
25, and it adds six names that do not exist — `remotehttp.Peer`, the widened
`WithRequestHook`, `remote.CallInfo`, `remote.HookError`,
`auth.CredentialFrom`, `port.ScopedReads` (with
`port.RelationsScopedRemotely`) and `port.EachPage`. What it does not do is
change the shape: the reads keep their signature, the options keep their
vocabulary, and nothing about the first block has to be unwritten to get to the
second. Two of the knobs do change the call *site* — `customersRO` is a second
identifier, and `EachPage` is a callback where `GetAll` was a slice — and the
code block says so rather than the prose, because that is where a reader looks.

`auth.CredentialFrom` does **not** exist today. This proposed forwarding example
is conditional on Auth accepting and recording its D-055 amendment for an
in-flight credential beside the principal; until then a Remote hook needs a
consumer-owned context value or the application must not forward credentials.

**A peer base is validated once, before traffic.** `TryTransport` must parse an
absolute `http`/`https` URL, require a host and a mount-prefix path, reject
userinfo, query, fragment, and encoded separators, and normalise only the final
slash. `Transport` may panic on the same invalid input. The default client must
refuse every redirect; a caller that intentionally follows one supplies its own
`http.Client` and accepts responsibility for credential/header forwarding. Error
paths redact any authority credentials even if a foreign transport supplied one.

### Why this shape

The common case is already two statements, and that is the part worth
protecting. What a framework loses points for is the second Tuesday: the call
site that was short stays short, and the thing you now need — a header, a scope,
a walk — is another line rather than a different construction.

The alternatives cost more than they look. A builder or a client struct puts
ceremony in front of the common case to serve the uncommon one. A generated
client per resource adds a build step and a second vocabulary for the same
model, and the whole point here is that there is no second vocabulary. Options
that overwrite each other, as `WithRequestHook` does today, are the worst of
both: the call site stays short and the behaviour is wrong.

**One hook option, widened, not two.** `WithRequestHook` gains a second
parameter and starts appending. Shipping a `WithCallHook` beside it would leave
two options feeding one hook list with different composition histories, which is
worse than the breaking rename — and the rename is free only until the tag. That
choice is made here and stated in H-REMOTE-06, H-REMOTE-18 and H-REMOTE-25
rather than left as an either/or in three places.

**`ScopedReads` is named for what it can do, and the name is smaller than it
sounds.** `port.Repository` is eight methods. `Get`, `GetAll` and `Count` take
options a predicate can ride on; `GetByID` accepts options and drops the
narrowing ones (H-REMOTE-02(4)); `Update` refuses them over a remote resource
for a good reason of its own; `Save` and `Delete` take none. A wrapper that
scoped the reads and forwarded the writes untouched would be
`crudnet.WithScope`'s asymmetry under a new name — reads 404 on another tenant's
row while a delete of the same row answers 200 — and that option's own comment
already says why that is not protection. So the writes refuse. Three things the
proposal has to say out loud, because leaving any of them silent is how a
smaller promise gets read as a bigger one:

- **`GetByID` cannot be scoped by adding an option.** It has to route through
  `Get` with an equality on the key plus the scope predicate, which is exactly
  what `security.gate.loadScoped` already does in this process
  (`crud/decorators/security/security.go:279-302`) — the same close H-REMOTE-02
  needs, so one fix serves both. A `ScopedReads` that added `crud.Where` to
  `GetByID` and shipped would be the *false* protection this paragraph is
  arguing against.
- **The write refusal needs a class, not a plain error.** A scoped resource
  mounted on `crudnet` is a gateway whose POST, PUT, PATCH and DELETE all refuse,
  and `port.KindOf` reads an unrecognised error as internal — so an unclassed
  refusal makes every write on that gateway a 500. It has to be an `*errs.Fault`
  carrying `errs.KindForbidden` and wrapping an exported sentinel
  (`port.ErrScopeCannotCross`) so `errors.Is` works. And it should be said in
  the same breath that a `ScopedReads` resource mounted on a binding is a
  read-only API by construction.
- **The wrapper is formally asymmetric.** `Get`, `GetAll`, and `Count` append
  the scope; `GetByID` routes through scoped `Get`; preloads are refused unless
  `RelationsScopedRemotely` was declared; `Save`, `Update`, `Delete`, and bulk
  delete refuse with that classified error; and `Next()` returns the wrapped
  repository's next decorator unchanged. The last rule is forwarding, not an
  escape from scope: walking past the wrapper consciously changes the value used.
- **It stops an honest mistake and nothing else.** The far service has no idea
  the narrowing was meant to be mandatory; the wrapper moves the trusted call
  site one layer up inside the same process. H-REMOTE-07's must-hold (3) — a call
  site is not where tenancy is enforced — is not closable at this interface by
  anything a caller can build. The enforcement is the owning service's gate,
  reached through the identity H-REMOTE-06 forwards.

It lives in `port` and not in `remote` because nothing about it is remote: it
decorates `port.Repository`, which `crud.Repo` and `specs.Repo` also satisfy
(`port/repository.go:9-11`), and a local repository behind a gateway wants the
same wrapper. Putting it in `remote` guarantees a second copy the first time
somebody needs it locally. (A generated service is *not* an example of that: it
satisfies `port.Service`, whose methods take commands, and it *takes* a
`port.Repository` — `test/versionstore/vv_gen.go:85-95`.) Its scope function is
`func(context.Context) (crud.Predicate, error)` — `security.Policy.Scope`'s
signature exactly (`crud/decorators/security/security.go:56-62`) — so moving a
resource in or out of this process changes one word at the wiring and nothing in
the policy. It deliberately does not return `[]crud.Option`, which would let a
caller hand back `crud.NarrowRelations` and fail at the far end of a code path
nobody expected to fail.

**`EachPage` is a callback and not an iterator, and that is the boring construct
on purpose.** There is no `iter.Seq` anywhere in this repository; introducing the
first one for a paging helper is a new vocabulary for no gain. Its contract is
H-REMOTE-08's must-holds (5) and (6) rather than design commentary: it walks
`NextCursor` while the page has items, it stops on an empty page rather than on
`HasNext` — the field H-REMOTE-08 proves can lie — and it refuses at the first
page when items came back with no cursor. That refusal is rarer than it sounds
and has to name its own remedy, because the three things that cause it are
specific: the far side declared `UnstablePagination()`, the caller passed
`crud.Unsorted()`, or the sort hops a relation. "Put the key in your `OrderBy`"
closes the first two; the default primary-key tiebreaker
(`crud/sqlrepo/repository.go:540-556`) is why everything else already walks.

**Cancellation and partial progress are part of the callback contract.** Before
each page `EachPage` checks `ctx.Err()` and returns it without another request;
if cancellation happens while the callback handles page N, the callback's error
wins and no page N+1 is fetched. Pages already handed to the callback stay
processed — no rollback or hidden retry occurs — so all-or-nothing exports need
their own transaction/sink protocol around the callback.

### What it must not break

- [[D-053]] — three answers and never a fourth. This is the decision the module
  is built on and `GetByID` already breaks it (H-REMOTE-02(4)): a narrowing
  option is neither honoured, refused nor documented. Whatever ships, that has
  to become one of the three. `ScopedReads` must not become a way to smuggle an
  option across either; what it adds goes through `ToRequest` like any other
  option.
- [[D-007]] and [[D-053]] again — `crud.Where` narrows the root table only.
  `ScopedReads` must refuse a call that carries a preload unless its
  construction says the far service scopes its own relations, which is what
  `port.RelationsScopedRemotely()` is doing in the code block above rather than
  in a footnote. Without it, it is the leak `NarrowRelations`'s refusal exists to
  prevent, one layer up.
- [[D-061]] — a wrapper forwards what it wraps. `ScopedReads` would be the first
  decorator at the `port.Repository` seam, so it needs `Next()` from the first
  commit rather than after the first thing that has to walk past it.
- [[D-030]] — a new verb on a seam is an obligation on every decorator, held by a
  test and not by memory. That test exists for `crud.Core` only: `coreVerbs` in
  `crud/decorators/security/obligation_test.go`, compared by reflection against
  `crud.Core`'s method set. A ninth method on `port.Repository` would be
  inherited by `ScopedReads` unscoped with nothing failing, which is the exact
  failure D-030 was written for. A `portVerbs` table belongs in the same commit
  as the wrapper — cheaper before it exists than after.
- **[[D-060]] — Query owns the pending cap-authority migration.** Its proposed
  amendment makes `port.Rules.PageCap` authoritative, retains
  `sqlrepo.MaxLimit` only as a compatibility backstop, and requires every binding
  to forward one resolved cap (`docs/ai/usecases/modules/query/Query.md:931-950`).
  Remote does not introduce another cap owner: under that pending contract,
  `GetAll` requests the far endpoint's declared unpaged/export capability and
  refuses if it is absent or capped; it never returns a truncated collection as
  “all”. Proposed `port.EachPage` is the explicit multi-request alternative and
  walks within the peer's declared page size. Until D-060 is amended, this is
  migration guidance, not a claim about current `GetAll` behaviour.
- [[D-047]] — a fault's `Error()` is classification only. Every far-side refusal
  this module surfaces reads as `errs: bad_request: bad_query (1 violation)` in a
  log, and the fix is **not** to put the violation message into `Error()`; that
  is exactly what the decision forbids. The close that survives it is for
  `remote` to recognise the refusals it knows about and wrap them in its own
  message.
- [[D-040]] — the framework does not retry. Retries stay in the caller's
  `http.Client`, **for reads**. An idempotency key on a create is not a retry
  policy and does not challenge this decision; it is what makes the caller's own
  retry safe, and it needs the hook to know which call it is on.
- [[D-012]] — `PUT` replaces and never creates. `remote.Save` with a set key
  therefore does not match `crud.Core.Save`. This is not a challenge to the
  decision and must not be "fixed" at the client: the gap is that it is not
  written down.
- [[D-055]] — a principal is a value in the context and the library never puts it
  there. Keeping the parsed `auth.Credential` beside the principal is the same
  move as keeping the principal, and it belongs in `auth.Guard`
  (`auth/guard.go:113`), not in an HTTP binding — one opt-in reaches all four
  transports. It does not contradict the invariant; it widens what `auth`
  expects to find in a context it does not own, which is close enough to the line
  to need an explicit yes rather than silence. And it is what a
  `ForwardHeader` would relay, so the same paragraph has to say that relaying a
  caller's own credential is for peers inside the estate.
- [[D-048]] — the contract manifest is closed. `port` cannot reach `auth` for the
  401's sentinel (H-REMOTE-15), so that close is a new name in `crud` or `errs`
  and a decision, not an option.
- [[D-062]] — nothing writes to a process-wide logger. None of this logs, and
  `port.Logger(ctx)` stays available and unused here, which is correct — but
  H-REMOTE-25 is what a consumer needs instead, and the module owes them a
  sentence about where outbound telemetry comes from.
- Nothing above needs `remote.Resource` to become a `crud.Core`. It must not:
  `Tx` is a promise a stateless call cannot keep.

## DX verdict

| What the ideal asks for | What you write today | What the close costs |
|---|---|---|
| One line to construct a remote resource | Exactly that | none |
| One peer, many resources | One `Transport(...)` per resource, each repeating host, client, hooks and cap by copy and paste | small — a peer constructor |
| Holding the far service's model | A copy of another team's struct, and a patch DTO (or `struct{}`, undocumented). 15–30 lines of declarations, and nothing detects drift in either direction | large — it is a decision, not code |
| The same read call as a local repository | Identical, filter and sort included | none |
| A narrowing option on `GetByID` | Accepted and thrown away. Nothing to write instead; the only safe advice is not to pass one | small — route through `Get`, or refuse |
| The same error branches | Identical for not-found, conflict, stale, validation; the 401 goes through `AsFault` | small doc · large if it needs a new sentinel |
| What cannot cross, refused by name | Exactly that, before anything is sent | none |
| Knowing what the peer will accept | Read their `Define` call, or send it and dig the message out of the fault | small doc · large as a wiring assertion |
| A narrowed preload on `GetByID` | Refused over HTTP, accepted over gRPC, documented in a flow doc | small |
| Many rows by key | Works — `crud.In` — and no doc says so, so the loop is what gets written | small (as a document) |
| A header per request, from the context | Your own context key, extraction, inbound stash and header — about 20 lines across two packages — and you get **one** hook | small |
| Two concerns adding headers | The second `WithRequestHook` silently deletes the first | small (one-line fix, invisible failure) |
| A hook that knows which call and which resource | Not possible; re-parse the method and path, and there is nothing to re-parse for the resource | small (one signature, breaking, free before a tag) |
| A tenant scope declared once | Nothing. `crud.Where` at every call site — which leaks on `GetByID` — or ~150 lines of your own wrapper that can scope three of eight methods | large |
| Every row, for a job | `GetAll`, silently truncated wherever the far side declared a page size, with a page that says it is complete and a godoc that says the local call behaves the same | small client-side · larger for both halves |
| A cursor walk | Works, as far as anyone can tell; nothing walks one in a test, and the sending side's nulls placement is unasserted | small |
| Writing more than one row | One round trip per row, and nowhere that says so | small (as a document) |
| A local write and a remote write | Guesswork, and the safe version needs an idempotency key that does not exist | small (as a document) |
| A retried create that does not duplicate | No idempotency key, and the docs recommend the retry | small |
| A misconfiguration found at wiring | Found on the first call, and a JSON 200 impostor is never found at all | small |
| A caller deadline longer than 30s | Silently cut to 30s — and the documented workaround removes the bound entirely | small (one edit closes three rows) |
| Connections reused on a hot path | Two idle connections per host, and nothing says to change it | the same edit |
| A peer that is down, branchable | `*url.Error` by hand; no kind, so a gateway answers 500 | small |
| A peer that is throttling | Indistinguishable from a broken one, and `Retry-After` is discarded | small |
| Telemetry per resource and per method | An instrumented round tripper that re-parses the URL, and no resource label at all | small, once `CallInfo` exists |
| Two services on two release trains | Nothing negotiates anything, and nothing says what is shared | large — a decision that hardens at the tag |
| Serving it again as a gateway | One line, and proven at the origin; the path hop is undocumented and `Meta()` is the client's copy | none to small · the drift half is large |
| Faking it in a test | One method for a transport; ~150 hand-written lines for a repository | small |

**Overall:** For the thing this package was built to do — a filter, a page, and a
refusal that keeps its class across a network — the code *is* the ideal, and the
tests are the good kind: a real client against a real binding on a real server,
with controls that fail when the assertion stops proving anything. The distance
opens in two directions the first round did not separate. The first is inside the
process: identity is reachable, capped at one hook, and shown nowhere; tenancy
has no seam and cannot be given a complete one here; and `GetByID` quietly drops
the one option that would have made the call-site workaround safe. The second is
everything the far service decides — its query policy, its page cap, its
presenter, its release date — which is most of what a consumer will actually hit
and almost none of what the module currently talks about. Customising rarely
means abandoning the short path; the two places it does are `ScopedReads`, which
splits one handle into two, and `EachPage`, which replaces the shortest call in
the package with a callback. Both of those are honest trades. The rest of the
remaining rows are not code at all, and they are cheap to write down and
expensive to discover.

## Release blockers found here

Severities: **blocker** stops a tag · **serious** is wrong behaviour a consumer
will hit · **sharp edge** is right behaviour that reads as a bug · **documentation**
is a wrong or missing sentence · **cross-reference** is another module's, named
here because it surfaces here.

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `GetAll` comes back truncated wherever the far side declared a page size, and the page it discards claims to be complete (`remote/resource.go:112-127`, `crud/options.go:242-247`, `crud/sqlrepo/repository.go:217-218`) | blocker | Silent partial data in exports and reconciliation jobs. Query’s **pending D-060 migration contract** is the canonical proposed authority: it requires an export-capability request and refusal, never a truncated “all” result (`docs/ai/usecases/modules/query/Query.md:939-951`). The client-only half needs no far-side deploy: the far side's clamp is on the wire as `PaginatedResponse.Limit` (`crud/page.go:8`), and `GetAll` is already decoding and throwing it away |
| 2 | A narrowing option on `GetByID` is silently dropped where the local call honours it: `port.NarrowForEntity` wipes `Filter`, `Terms`, `Search` and `Sort` (`remote/resource.go:139`, `port/request.go:38-44`) against `crud/sqlrepo/repository.go:144-150` | blocker | `GetByID(ctx, id, crud.Where(crud.Eq("TenantID", 7)))` is `ErrNotFound` locally and another tenant's row remotely, over a 200. It is the fourth answer [[D-053]] says does not exist, inside the module [[D-053]] was written for, and it falsifies the only workaround available for blocker 3 |
| 3 | `security.Gate` cannot wrap a remote resource, and the obvious wrapper cannot close it: `port.Repository` carries usable options on three of eight methods (`port/repository.go:16-25`, `remote/resource.go:212`, and blocker 2) | blocker | Tenant isolation over a remote resource has no seam, and the shape everyone reaches for first — scope the reads, forward the writes — is the false protection `crud/http/crudnet/options.go:87-96` already warns about. The honest close is a smaller, named promise: reads scoped, writes refused with a rendered class, and enforcement left to the owning service's gate |
| 4 | [[UC-018]] is marked `covered` with an empty gap list (`docs/ai/usecases/Index.md:89`), while blocker 1 falls inside its guarantee 3, blocker 2 inside guarantee 2, blocker 3 inside guarantee 11 and blocker 5 inside guarantee 7 | blocker | The use-case tree is what the next reader trusts instead of re-deriving. Either these findings are wrong or UC-018 loses `covered` and gains gap rows — deciding which is the one thing a release sweep owes the owner |
| 5 | Any 2xx body is decoded with no content-type check and no required key, on **every** method (`remote/remotehttp/transport.go:156-158`, `remote/resource.go:280-289`) | serious | A JSON 200 from a proxy, a stub, or a peer using `crudnet.WithTransform` becomes an empty page, a zero-valued row that "exists", a count of 0, a delete that reports nothing gone, or a zero written back over the caller's model. One decode change closes this and H-REMOTE-13(3) — price it once. `decode`'s own comment states the requirement it misses |
| 6 | A whole-row `PUT` writes zero over any column the client's copy of the model does not have (`remote/resource.go:186-200`, `crud/http/crudnet/handler.go:333-335`) | serious | The model is a hand copy of another team's struct and nothing detects drift. 200, a refreshed model that looks right, a column emptied on somebody else's row. No cheap library fix — it needs a stated position on how the model is meant to be kept in step |
| 7 | The request hook is the wrong shape twice: `WithRequestHook` assigns instead of appending (`remote/remotehttp/transport.go:91`) and cannot tell which call or which resource it is on (`:90-92`, `:117`) | serious | Auth plus tracing is the normal wiring and the dropped one fails invisibly; and no idempotency key, metric label or per-resource branch is possible without re-parsing a URL. One signature change closes both — breaking now, free before a tag, a deprecation after |
| 8 | A dead peer, a TLS failure, a timeout, an over-cap answer and a hook that could not mint a token are all unclassified plain errors; 429 is `KindInternal`; `Retry-After` is discarded (`remote/remotehttp/transport.go:133-155`, `port/porthttp/decode.go:22-41`) | serious | A gateway answers 500 where 503 is the truth, throttling is indistinguishable from breakage, and vv's own client throws away the header vv's own renderer wrote (`port/porthttp/render.go:146-148`) |
| 9 | The only deadline in the module lives in `defaultClient()` (`remote/remotehttp/transport.go:71`), so a caller's longer deadline is cut to 30s **and** the documented workaround — pass your own client — removes the bound altogether | serious | The export job of blocker 1 is the caller that trips the first half, and the module's own test builds the unbounded client as a control (`remote/remotehttp/transport_test.go:37`) without noticing. Two docs assert the opposite, one of them a test comment (`transport_test.go:87-88`) whose body proves something else — the vacuous verdict [[D-020]] exists to refuse. One edit closes this and row 10 |
| 10 | `defaultClient()` has no `http.Transport` of its own, so a hot resource gets `MaxIdleConnsPerHost` 2 (`remote/remotehttp/transport.go:71`) | sharp edge | The most common first-quarter production surprise of the set: the peer is healthy, this process is in TCP and TLS handshakes, and nothing at the wiring says to pass a tuned client. Same constructor, same edit as row 9 |
| 11 | `GetAll`'s doc comment claims parity with a local `GetAll` that is capped (`remote/resource.go:109-111`), which [[D-060]]:68-70 and `TestGetAllIsNotCappedByMaxLimit` both say is false | sharp edge | It sits on the method that causes blocker 1, and a reader who checks the godoc before writing the export job is told the truncation is normal. One line, and it is not covered by blocker 1's own close |
| 12 | The base URL is never parsed; a missing scheme fails on the first call, not at wiring (`remote/remotehttp/transport.go:40`) | sharp edge | Every other declaration mistake in this library is a start-up failure ([[D-021]]); this one waits for traffic. `TryTransport` beside a panicking `Transport` is the existing precedent |
| 13 | The peer's `query.Config` is a second contract with ten bounds, invisible from this side and assertable nowhere (`crud/query/compile.go:29-82`) | sharp edge | A filter, an `in` list, a nesting depth or a preload count that works locally 400s over the wire, decided by a config struct in someone else's deployment — and the refusal that names the fix does not render (row 17) |
| 14 | Behaviours documented nowhere a caller looks: a set key does not create over the wire ([[D-012]]), a narrowed preload is refused on `GetByID`, a delete of a missing row is `(0, nil)`, there is no batch write, `crud.In` is how you read a set of keys, this only talks to a vv peer, and nothing negotiates a version between two deployables | sharp edge | Each is a correct decision or a real precondition that reads as a bug on first contact, and most of them are silent. The version one is the only item on this table that gets harder to fix after the tag rather than before |
| 15 | No hook test, no cursor-walk test, no nulls-placement test on the sending side, no `Partial` assertion; `_examples/` contains nothing about the consuming half | sharp edge | [[D-020]] makes tests the specification, and four of the paths a consumer uses first are specified nowhere |
| 16 | `docs/modules/en/remote.md` names the patch DTO `ArticleInput` in all three examples (`:24`, `:36`, `:152`) while `cmd/vv` emits `<Model>Update` (`internal/codegen/render.go:139-142`) and the same doc says to use the generated one; and `remote/remotehttp/transport.go:89` links `[AcceptLanguage]`, which is `porthttp`'s symbol, not this package's | documentation | The first code a consumer copies declares a type the generator never produced, and the option they reach for first has a dead link on its published doc page |
| 17 | Not this module's, filed here because it surfaces here: every far-side refusal reads as `errs: bad_request: bad_query (1 violation)` because `Fault.Error()` renders classification only (`errs/fault.go:68-98`) — the unpaged refusal, the bulk-delete cap (`crud/http/crudnet/handler.go:394-395`) and every `query.Config` bound alike | cross-reference | [[D-047]] forbids enriching `Error()`, so the fix belongs to `errs`' own sweep, or to `remote` recognising the refusals it knows about and wrapping them. Two more items file the same way: a 401 sentinel would need a new name in `crud` or `errs` because `port` cannot reach `auth` ([[D-048]], `Makefile:37`), and `crudnet.WithTransform` (`crud/http/crudnet/options.go:81-85`) needs a line saying it breaks every vv client holding that resource — in the four bindings, where the serving team will read it |

## Contested

- **Blocker 1 keeps `blocker` severity although it fires only where the far side
  declared a `MaxLimit`.** A reviewer was right in round 1 that a stock far end
  returns every row, and the wording now says so. The severity stays: Query’s
  pending D-060 migration contract identifies refusal rather than truncation as
  the proposed compatibility outcome, and the defect fires exactly where an
  operator did the careful thing. A narrower blast radius makes the argument
  sharper, not smaller.
- **H-REMOTE-01 stays ✅ despite the peer's `query.Config`.** A reviewer wanted it
  re-rated because a filter can 400 over the wire. Its must-holds are about the
  translation — that what this client sends is the narrowing a local repository
  would have received — and that is true and well tested. Whether the *peer*
  accepts it is a different promise with a different owner, so it is
  H-REMOTE-19 with its own ❌ and its own blocker row, rather than a footnote that
  makes the translation look broken.
- **Blocker 17 stays in the table, and the convention is now consistent.** Two
  reviewers disagreed about whether another module's behaviour belongs here.
  It does, marked `cross-reference`, because it is reachable only through this
  module's first-day failure — and H-REMOTE-15's 401 sentinel and
  `crudnet.WithTransform` are now filed the same way in the same row rather than
  one of them left unmarked.
- **H-REMOTE-02 keeps a must-hold about a row the far service hides.** A reviewer
  said to drop it as unfalsifiable from the client side. It is kept and written
  as an obligation the client does not own, because a consumer arriving here does
  ask the question — [[D-008]] is one of the guarantees they are buying — and
  answering "that is the far service's, and here is what reaches you either way"
  is more use than silence. What is dropped is the first round's claim that this
  module has coverage for it.
- **`ScopedReads` refuses writes at runtime rather than being a read-only type.**
  A reviewer noted this sits badly beside [[D-021]]'s start-up failures and asked
  for the choice to be argued. It is argued: a read-only type is not a
  `port.Repository`, so it cannot be mounted, decorated or handed to anything
  that takes one — which removes the composition that makes the wrapper worth
  having. The runtime refusal is the trade, and the price of it is that the
  refusal must carry a class a gateway can render, which is now part of the
  proposal rather than left unstated.
- **Foreign-envelope recognition is a Port pointer, not a second Remote parser.**
  Remote correctly turns a body `porthttp.ParseEnvelope` declines into
  `ProtocolError` (`remotehttp/transport.go:242-255`). The false acceptance of a
  foreign `{"type":"error"}` body is therefore owned by Port's envelope parser;
  Remote owns only the caller-facing protocol-error recovery and does not invent
  another acceptance grammar.

## Edge cases

### E-REMOTE-01 — The configured peer URL carries userinfo
**Shape:** adversarial input | misuse
**Setup:** An environment variable contains `https://client:secret@content.internal/articles`, and the address is wrong or the peer returns a non-vv error.
**What the consumer does:** They expect an error safe to put in an application log. A URL credential may be unwise, but configuration parsers and legacy endpoints still produce one; the library must not make that secret observable.
**What must happen:** `Transport` must reject URL userinfo at wiring, or redact it from every request-building and protocol error.
**Today:** ❌ wrong or unhandled
**Evidence:** `remote/remotehttp/transport.go:39-46` stores the supplied base string unchanged; `:117-159` puts the resulting target in the fault location; `fault` retains it at `:247-255`; and `remote/transport.go:93-100` includes `Where` in `ProtocolError.Error()`. `remote/roundtrip_test.go:329-375` tests a wrong URL but no URL with userinfo.
**Blast radius:** data leak

### E-REMOTE-02 — A resource base URL has a query or fragment
**Shape:** misuse | degenerate declaration
**Setup:** A config value is copied from a browser or health-check URL, such as `https://content.internal/articles?canary=blue` or `https://content.internal/articles#v2`.
**What the consumer does:** They expect a resource-prefix constructor to reject a URL that cannot safely have a method path appended to it.
**What must happen:** Construction must parse the base URL and refuse query, fragment, and other non-prefix forms before the first call.
**Today:** ❌ wrong or unhandled
**Evidence:** `remote/remotehttp/transport.go:39-46` only strips one trailing slash; `Do` constructs a target by string concatenation at `:117-124`. There is no URL parse or constructor-error path, and `remote/remotehttp/transport_test.go:23-42` covers only an ordinary base URL.
**Blast radius:** confusing error

### E-REMOTE-03 — The peer answers a redirect instead of a CRUD response
**Shape:** adversarial input | seam
**Setup:** An ingress redirects an old mount to a login page or another host, and the caller's hook has prepared the request for the configured peer.
**What the consumer does:** They expect this fixed-peer client to report a protocol failure at the configured address, rather than silently pursuing a different route or origin.
**What must happen:** The default transport must refuse redirects and report the original 3xx response; following redirects needs an explicit opt-in at the caller's `http.Client` seam.
**Today:** ❌ wrong or unhandled
**Evidence:** the default is only `&http.Client{Timeout: DefaultTimeout}` at `remote/remotehttp/transport.go:64-71`, with no redirect policy; a nil `CheckRedirect` uses Go's redirect-following default (`net/http/client.go:63-77`), and sensitive headers are forwarded to the same host or its subdomains (`:41-49`). `Do` delegates to `t.client.Do` at `remote/remotehttp/transport.go:138-141` and accepts every final 2xx response at `:156-159`. No remote test installs a redirecting server (`remote/remotehttp/transport_test.go:23-100`; `remote/roundtrip_test.go:36-518`).
**Blast radius:** silent wrong answer

### E-REMOTE-04 — A 202 or 206 carries syntactically valid JSON
**Shape:** adversarial input | seam
**Setup:** A proxy returns `202 Accepted` before a write completed, or `206 Partial Content` containing a valid but incomplete page.
**What the consumer does:** They expect a repository method to return only the completed full document its method promises, not treat every successful-looking HTTP status as equivalent.
**What must happen:** Each call must accept the statuses the vv route actually promises and reject other 2xx statuses as a protocol error; a 201 create may be valid, a 202 or 206 is not silently interchangeable with it.
**Today:** ❌ wrong or unhandled
**Evidence:** `remote/remotehttp/transport.go:156-159` accepts the entire 200–299 range without consulting `remote.Call.Method`; `remote/resource.go:280-288` only unmarshals the body shape. The round-trip suite exercises the binding's ordinary responses (`remote/roundtrip_test.go:36-192`), not 202 or 206.
**Blast radius:** silent wrong answer

### E-REMOTE-05 — A string primary key contains a path separator
**Shape:** boundary | adversarial input
**Setup:** A remote resource uses human slugs or external IDs, and a valid key contains `/`, `%`, a space, or non-ASCII text.
**What the consumer does:** They expect that key to name one row, never a different route or a differently decoded ID.
**What must happen:** A single-row call must encode the primary key as exactly one URL path segment on Get, Update, Replace, and Delete.
**Today:** 🟡 partial
**Evidence:** `port/request.go:46-72` turns the typed key into text, and `remote/remotehttp/transport.go:174-193` applies `url.PathEscape` to every entity-route ID. The supplied integration model has only an `int64` key (`remote/fake_test.go:20-26`), and no transport test exercises an escaped string key.
**Blast radius:** confusing error

### E-REMOTE-06 — A resource receives no transport
**Shape:** misuse | degenerate declaration
**Setup:** A conditional wiring branch has no configured peer and supplies a nil `remote.Transport` to `remote.New` or `remote.TryNew`.
**What the consumer does:** They expect the declaration to fail before any repository method can dereference a nil collaborator.
**What must happen:** `TryNew` must return an explanatory error and `New` must fail immediately, preserving the framework's start-up-error convention.
**Today:** 🟡 partial
**Evidence:** `remote/resource.go:51-62` routes `New` through `TryNew`, and `TryNew` rejects a nil transport before reflection or a request. No remote test calls either constructor with nil (`remote/roundtrip_test.go:36-518`).
**Blast radius:** none

### E-REMOTE-07 — Optional HTTP wiring receives nil or a non-positive cap
**Shape:** misuse | boundary
**Setup:** A config branch supplies `WithClient(nil)` and an environment parser produces `WithMaxResponse(0)` or `WithMaxResponse(-1)`.
**What the consumer does:** They expect the client to remain bounded and usable, not to panic on the first call or silently make response reading unlimited.
**What must happen:** A nil client must leave the owned default in place, and every non-positive response limit must use the documented finite default.
**Today:** 🟡 partial
**Evidence:** `remote/remotehttp/transport.go:79-85` ignores a nil client; `WithMaxResponse` records its argument at `:94-99`; and `cap` substitutes `MaxResponse` unless the value is positive at `:108-114`. `remote/remotehttp/transport_test.go:23-42` tests an explicit non-nil replacement, and `:50-85` tests a positive cap; neither covers these degenerate values.
**Blast radius:** none

### E-REMOTE-08 — A job has no keys to delete
**Shape:** boundary
**Setup:** A retention job's selection is empty and calls `Delete(ctx)` with no IDs.
**What the consumer does:** They expect a truthful zero and no pointless remote mutation request.
**What must happen:** The call must return `(0, nil)` without contacting the peer.
**Today:** ✅ handled
**Evidence:** `remote/resource.go:237-263` returns before `Transport.Do` for zero IDs. `remote/roundtrip_test.go:182-191` pins both the zero result and absence of a recorded far-side call.
**Blast radius:** none

### E-REMOTE-09 — Save is called with a nil model pointer
**Shape:** misuse | partial failure
**Setup:** A handler passes a nil `*M` after a failed decode or a conditional allocation path.
**What the consumer does:** They expect a clear local error and no `null` create or replace request.
**What must happen:** The model must be rejected before `Transport.Do`; a malformed caller value cannot become a remote write.
**Today:** 🟡 partial
**Evidence:** `remote/resource.go:173-192` marshals first but then calls `Meta.HasID` before constructing or sending a call; `crud/access.go:8-14` rejects a nil model pointer. No Save test passes nil (`remote/roundtrip_test.go:100-130`).
**Blast radius:** none

### E-REMOTE-10 — The request is already cancelled before token minting
**Shape:** partial failure | seam
**Setup:** An inbound request is cancelled before the gateway starts a remote call; its `WithRequestHook` mints or exchanges a token before setting the header.
**What the consumer does:** They expect cancellation to prevent this configured
request hook from minting a token after the caller has gone away.
**What must happen:** `Do` must check `ctx.Err()` before invoking the hook (and
again before the client), returning cancellation without this hook work or a
send. This case makes no broader preflight claim than the source supports.
**Today:** ❌ wrong or unhandled
**Evidence:** `remote/remotehttp/transport.go:117-136` builds the request and executes the hook unconditionally before `t.client.Do`; only the latter observes cancellation at `:138-141`. `TestTheCallersDeadlineReachesTheRequest` cancels a context but installs no hook (`remote/remotehttp/transport_test.go:87-100`).
**Blast radius:** confusing error

### E-REMOTE-11 — One resource is used by many concurrent requests
**Shape:** concurrency
**Setup:** A gateway shares one `remote.Resource` across many requests with different IDs, options, contexts, and response bodies.
**What the consumer does:** They expect the resource to be safe to share: one call's query, hook-created request, and decoded response cannot bleed into another's.
**What must happen:** Resource and transport configuration must be immutable after construction and each call must allocate its own request, route data, and decode target; the invariant needs concurrent coverage.
**Today:** 🟡 partial
**Evidence:** `Resource` retains only a transport and meta at `remote/resource.go:41-45`, while each repository method creates local calls and decode targets (`:94-105`, `:130-145`, `:173-201`); `remote/remotehttp/transport.go:117-159` likewise holds per-call route, request, and response values locally. Go documents `http.Client` as safe for concurrent use (`net/http/client.go:30-35`). The tests are serial; no remote test runs shared-resource calls concurrently (`remote/roundtrip_test.go:36-518`; `remote/remotehttp/transport_test.go:23-100`).
**Blast radius:** confusing error

### E-REMOTE-12 — An extension transport answers success with no document
**Shape:** boundary | seam
**Setup:** A consumer writes a minimal `remote.Transport`, or a wrapper accidentally drops an otherwise successful response body and returns `(nil, nil)`.
**What the consumer does:** They expect every resource method to fail loudly instead of returning a zero-valued page, row, count, or delete count as though the peer answered it.
**What must happen:** The shared decoder must reject an empty successful document before a caller can observe a zero value.
**Today:** 🟡 partial
**Evidence:** `remote/transport.go:56-67` makes custom transports a supported extension point, and `remote/resource.go:274-288` rejects `len(raw) == 0` before unmarshalling. The HTTP cap test covers a non-empty response (`remote/remotehttp/transport_test.go:50-85`), but no remote-resource test supplies an empty successful `json.RawMessage` from a custom transport.
**Blast radius:** none

### E-REMOTE-13 — The peer commits a write but the response is lost
**Shape:** ambiguous outcome | retry boundary
**Setup:** `Save` creates an entity, or `Delete` removes one, the peer commits,
and the connection drops before the response reaches the caller. A job retries
the same method because it received a timeout/transport error.
**What the consumer does:** They need to know whether retry is safe, whether the
outcome is explicitly ambiguous, and where an idempotency key belongs.
**What must happen:** Remote must not retry writes automatically. `Save`/`Delete`
return an error that means "outcome unknown" when no response arrived; the
caller either reads state before deciding, supplies a peer-recognised idempotency
key through the widened call hook, or chooses not to retry. A `PUT` replace may
be idempotent only under the far service's documented semantics; POST create and
DELETE are not assumed safe merely because the method names look familiar.
**Today:** ❌ wrong or unhandled
**Evidence:** `Save` maps unset IDs to POST and set IDs to PUT
(`remote/resource.go:167-201`), while `Delete` maps one ID to DELETE
(`:226-262`). `remotehttp.Do` returns the raw client error after `t.client.Do`
fails (`remote/remotehttp/transport.go:138-141`) and has no idempotency-key or
ambiguous-outcome type; the hook sees only `*http.Request` today
(`:87-92,132-135`). No write-lost-response/retry test exists in the remote or
transport suites.
**Blast radius:** duplicate create / incorrect compensating action

## Edge verdict

The highest-severity new edge is a direct secret disclosure: a URL containing
userinfo is copied into `ProtocolError` and therefore ordinary application logs.
The fixed-peer contract is also porous: the default client follows redirects,
and any final 2xx status is accepted even when it cannot mean a completed CRUD
answer. The resource defends several ordinary declaration and boundary mistakes
in code — nil transport, non-positive response caps, zero-key delete, nil model,
and empty extension output — but all except the empty delete lack focused tests.
Cancellation does reach the HTTP request, yet it arrives too late to suppress a
costly per-request hook. A lost write response has no idempotency or
ambiguous-outcome contract, so callers can retry a committed create/delete by
accident. Response caps and cancellation remain Remote-owned client behaviour;
shared-resource concurrency is plausible by inspection and still unpinned.

## Release blockers found here (edge)
| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | URL userinfo is preserved in a `ProtocolError` and its `Error()` string (`remote/remotehttp/transport.go:117-159`; `remote/transport.go:93-100`) | serious | A failed remote call can write an endpoint credential into normal application logs. Reject userinfo or redact every error before a service ships its first credentialed peer configuration. |
| 2 | The default client follows redirects and the transport treats any final 2xx as a normal resource response (`remote/remotehttp/transport.go:64-71`, `:138-159`) | serious | A call configured for one peer can silently complete against another route or origin, defeating the client’s fixed-peer and protocol-validation promises. |
| 3 | `WithRequestHook` runs even when the inbound context was already cancelled (`remote/remotehttp/transport.go:117-141`) | sharp edge | Token minting, tracing, or other hook work happens after the consumer has cancelled the call. The existing cancellation test proves only that an unhooked HTTP request stops. |
| 4 | A base URL with query or fragment is accepted and concatenated into the first request (`remote/remotehttp/transport.go:39-46`, `:117-124`) | sharp edge | A configuration typo cannot fail at wiring and produces a malformed or surprising route only under traffic. |
| 5 | A committed `Save`/`Delete` whose response is lost is indistinguishable from a write that never reached the peer; Remote has neither an ambiguous-outcome error nor an idempotency-key contract (`remote/resource.go:167-201`, `:226-262`; `remote/remotehttp/transport.go:138-141`) | serious | A well-meaning retry can duplicate a POST create or perform an incorrect compensating action after a committed delete. |
