# port · port/porthttp — one set of rules for a resource, and one shape for every way it can fail

**Covers:** `github.com/frostgrove/vv/port`, `github.com/frostgrove/vv/port/porthttp`
**Sweep:** happy paths · release readiness
**Verdict:** not ready — the transport-neutral half is finished and well pinned; the seam that installs it is not, the seam a consumer is told to stand on does not reach the delete verb or a domain error they already have, and every document in the repository that says how to install the error contract describes a call that configures nothing on a generated route.

## What a consumer is actually trying to do

They have twenty resources and two of them have a rule. A quota before a create,
an audit row after one, a field only an admin may patch, a row only its author
may delete. They do not want to abandon the generated endpoints for those two,
and they do not want the rule written once per framework. What they want is a
place to stand between the request and the database, written once, mounted
wherever the resource is mounted — including on the protocol the platform team
will ask for next quarter.

There is more than one such place, and they do not reach the same things. One of
them sees the model and every verb. One sees the framework's own request and
three verbs out of five. One narrows reads and silently does not narrow writes.
One sits under all of them, on the repository, and is the only one a `DELETE`
cannot walk past. Choosing wrong is not a compile error and not a test failure —
it is a rule that works in development and is missing on one route in
production. Which one to stand on is the first decision anybody makes here and
the one this module explains least.

They also have a client. A mobile app, a React form, another team's service. That
client needs to know which of the four ways a request can be wrong it was: the
row is gone, you are not allowed, that email is taken, that field is not a field.
It needs the answer as a status it can branch on and as a machine code it can
switch on, and it needs the field named in the words the client itself sent —
not the Go identifier the column has on the server.

They also already have errors. Forty of them, with names, raised by a domain
layer written before any of this. Adopting the error contract means those forty
have to arrive as something other than a blank 500, and that is the first
integration job after mounting.

Then there is the second week. Somebody adds an endpoint the generator did not
write — `POST /articles/{id}/publish` — and it has to fail the same way as its
neighbours. Somebody loads a French catalogue and expects the whole server to
speak French, including the 401 at the door. Somebody puts the whole thing behind
an API gateway and needs the calling service to recover the same error branch it
would have had in-process. None of these are exotic; all three land in the first
month.

And underneath all of it: nothing a client sends should be able to make the
process hold a gigabyte, nothing a driver says should be able to reach a response
body, and when a request does answer 500 somebody has to be able to find out why.
Those three are not features anybody asks for. They are what a person notices
only when they are missing.

## Happy cases

### H-PORT-01 — A quota check in front of create, and the routes stay generated
**Who:** the author of a SaaS whose free tier allows fifty articles
**Wants:** one method intercepted, nothing else touched.
**Story:** They declare a type that embeds the default service and write one
`Create`. It checks the tenant's count, refuses with a wrapped sentinel, and
otherwise forwards. They hand that value to the binding instead of the
repository.
**Must hold:**
1. Embedding costs no forwarding methods; overriding one method is the whole cost.
2. A refusal stops the request — no statement is issued.
3. The refusal's status is the sentinel's, and the service does not import the transport.
4. What the override mutates is what the repository writes.
5. Every way a row can be created enters the override. A `PUT` to a key that does not exist does not get in behind it.
6. The override is not handed a key or a timestamp the client chose.
7. An override of one verb is reached from the others. A `Get` that hides rows also hides them from the `PUT` that checks existence.
**Today:** 🟡 partial — 1 to 3 hold; 4 holds with a qualifier nothing states; 5,
6 and 7 do not.
**Evidence:** 1–3: `port/service.go:82` (`NewService`), and the embedding shape
runs against a live database — `test/integration/http_port_test.go:37` is the
override, and its control at `:159` ("and the control: a title it allows is
written") asserts a row is written and the client's key was dropped, so the
refusal leg cannot pass for a service that refuses everything.
(4) is true of everything except the fields the clearing owns, and the two facts
are in the same function. An override that mutates and then delegates last has
its mutation of the key or of a `generated` column erased, because `Sanitize` is
the *first* statement inside `DefaultService.Create` (`port/service.go:155`) and
`Replace` runs `ClearGenerated` at `:198`. `Sanitize` zeroes the PK when
`meta.PK.Auto && !allowClientID` and then clears the generated columns
(`port/model.go:14-21`).
(5) `port/service.go:190-212`: `Replace` calls `s.repo.Save` itself at `:209`,
and only guards with a `GetByID` when the key is database-generated and
`AllowClientID` is off (`:192`). Its own comment says the rest out loud — *"A key
the client owns (a uuid, a slug) is a different matter and is still created."* On
a uuid-keyed resource, `PUT /articles/{new-uuid}` creates a row without entering
the consumer's `Create`.
(6) `Sanitize` runs after the override, so an override that reads `cmd.Model.ID`
sees whatever the client sent. `grep -rn "Sanitize("` finds exactly two
non-test call sites — `port/service.go:155` and the forwarder
`crud/http/crudhttp/model.go:15` — and nothing in any binding sanitizes before
the service. The clearing-then-hook order is real, but it is the *hook's*
guarantee ([[UC-013]] guarantee 7), not the override's.
(7) is the rule the other two are instances of: **`DefaultService` never calls
its own interface methods.** `DeleteMany` calls `s.repo.Delete` and not `s.Delete`
(`port/service.go:234`); `Replace`'s existence guard is `s.repo.GetByID` and not
`s.Get` (`:193`). So an override of `Get` that implements a soft delete or a
visibility rule is walked past by `PUT` as well. Stated as two examples a
consumer checks two verbs; stated as the rule they check all eight.
**If not ready:** A consumer whose keys are uuids ships a quota a `PUT` walks
past, and one who hides rows in `Get` ships a `PUT` that finds them. Today they
override `Replace` and `DeleteMany` as well, and call
`port.Sanitize(s.Meta(), &cmd.Model, false)` themselves before reading anything
off the model — which they cannot write correctly if they also use
`AllowClientID`, because the flag is unexported with no accessor
(`port/service.go:72-79`, and the method set at `:101-235` is `Meta`, `Paths` and
the eight verbs). Closing 5 and 7 is a paragraph on `DefaultService`'s doc
comment saying it calls its repository and never itself. Closing 6 is either
moving `Sanitize` out of `Create` into the bindings (which [[D-045]] pushed it
*out* of, deliberately) or saying plainly that an override sees the request as
sent. An `s.Sanitized(&m)` helper would close the write-it-yourself half.

### H-PORT-02 — Which seam do I stand on?
**Who:** anybody with their first business rule and five places to put it
**Wants:** to be told which one reaches the verb and the data their rule needs.
**Story:** They have a rule. They read the option table on a binding page, see
something called a scope and something called a before-hook, pick one, and write
the rule. Three weeks later a route the rule does not cover is found by a
customer.
**Must hold:**
1. There is one place that says what each seam reaches and what it does not.
2. A seam whose name reads like protection and is not says so where the name is read, not only in a doc comment.
3. Nothing that sounds like a row-level rule is offered without the thing that actually enforces one.
**Today:** ❌ missing for 1; 2 holds in one place out of the places it is read.
**Evidence:** the five seams and their real reach, none of which is written down
in one place anywhere:

| Seam | Sees | Reaches | Written once per |
|---|---|---|---|
| a `Service` override | the model, the context (and so the principal) | all eight verbs, if you override each | process |
| `BeforeSave` / `BeforeUpdate` | the framework's own request | create, replace, patch — **not delete, not bulk delete** | binding |
| `WithScope` | the request | `List`, `Count`, `Get` — **reads only** | binding |
| `WithTransform` | the request and the model | the response, not the write | binding |
| `security.Gate` | the context and the stored row | every verb the repository has, delete included | process |

The principal is on the *context*, not on the request
(`auth/context.go:30`, `PrincipalFrom(ctx)`), and every binding installs it
there — Fiber deliberately so, because `crudfiber` hands `c.Context()` down and a
principal in `Locals` would be invisible to every policy
(`auth/http/authfiber/authfiber.go:11-22`). So a rule that needs only the
principal belongs on the Service and needs no per-binding seam at all. What
genuinely needs the request is a raw header, a path value or a query flag.
(2): `WithScope`'s doc comment spends six lines saying the thing its name denies
— *"with a scope of TenantID = 7, GET /{id} on somebody else's row is 404 while
DELETE /{id} on the same row answers 200"* (`crud/http/crudnet/options.go:91-96`)
— and every module page's option table lists it beside `WithQuery` and
`ReadOnly` with one line.
(3): `crud/decorators/security` is the answer for a row-level rule and is named
in that doc comment and nowhere a consumer reading `port` would look.
**If not ready:** They pick by name and find out from a customer. Closing it is
the table above, in `port/doc.go` and in `docs/modules/en/port.md`, plus
`ReadScope` as the option's name — a rename that costs one line before the tag
and says at the call site what the doc comment needs a paragraph for
(H-CRUDHTTP-03 proposes the same rename from the binding side).

### H-PORT-03 — The rule that has to read the request
**Who:** a developer with a field only an admin may patch, and a delete only an admin may issue
**Wants:** a header, or a path value, in front of one write.
**Story:** They try to override `Update` and find the command carries no request.
They find the hook instead, wire `BeforeUpdate`, and refuse. Then they write the
same rule for delete and find there is no hook to wire.
**Must hold:**
1. A seam that can see the request exists on every write verb.
2. Its refusal reaches the client as the status that refusal deserves.
3. Its mutation is what the repository writes, and the server-owned fields are already cleared when it runs.
4. A hook that fails after the row is written leaves no row behind — or the module says plainly that it does not.
**Today:** ❌ missing for 1; 2 is a live trap; 3 holds on two verbs of three; 4 is
answered in a use case and nowhere a consumer stands.
**Evidence:** (1) does not hold, and the half that is missing is the larger one.
`DeleteCommand` carries `ID` only and `BulkDeleteCommand` carries `IDs` only
(`port/command.go:66-74`); `Before` appears at `port/command.go:45`, `:53`, `:61`
and nowhere else. `grep -rn "BeforeDelete" --include="*.go" .` returns nothing,
and each binding declares exactly two hook options
(`crud/http/crudnet/options.go:101-109` and the three siblings). So *"only an
admin may delete"*, *"you may not delete a published article"* and *"write an
audit row on delete"* have nowhere to go but an overridden `Delete` — which
`DeleteMany` walks past (H-PORT-01(7)).
(3) holds on create and replace and not on the verb this case is about.
`DefaultService.Update` calls neither `Sanitize` nor `ClearGenerated`
(`port/service.go:170-179`), and the hook is handed the patch DTO rather than the
model (`port/command.go:50-54`), so "after the server-owned fields are cleared"
describes nothing on the `PATCH` path. On create and replace it is real and the
comment says why: `port/service.go:148-151`, `:155`, `:158`.
(2): the status comes from the kind, and `sentinelKind`'s default arm is
`errs.KindInternal` (`port/kind.go:126-127`) — so `fmt.Errorf("over quota")`, the
first thing anyone writes, is an opaque 500. It *is* documented, in a use case:
[[UC-015]] guarantee 7 says a hook refusal answers the refusal's own status and a
hook failure is a silent 500, and that the client can tell them apart. It is
documented where the consumer is not standing — the option's doc comment and
every module page's option table give the signature and say nothing about which
errors map to what (`docs/modules/en/crudnet.md:93-94`, `crudgin.md:90-91`,
`crudfiber.md:91-92`, `crudgrpc.md:86-87`). The sibling sweep records the same
trap as H-CRUDHTTP-11(3), unfixed.
(4): [[UC-013]]'s **Out of scope** says plainly that nothing here makes a hook and
the statement it precedes atomic and hands it to [[UC-005]]. There is no `After`
hook on any command and no seam from `port` onto `crud.InTx`
(`crud/executor.go:503`), so "check the quota, insert the row, write the audit
row" is three statements with a window between them unless the consumer captures
the executor itself.
**If not ready:** For delete, the answer is the security gate or middleware — a
different subsystem and a different mental model, and nothing here says so. For 2
the consumer writes `errs.Validation().Field("Age").Code(...)` or
`port.BadRequestf` once they have found the `errs` page; nothing points them
there from here. For atomicity they open the transaction in an overridden service
method and pass the transactional context down. One sentence on each hook's doc
comment closes 2; 4 is a paragraph on `port/doc.go`'s limits list, where the
other three stated limits already live; 1 is a `Before func(context.Context) error`
on the two delete commands and a fifth option on four bindings.

### H-PORT-04 — Only the author may edit or delete this article
**Who:** anybody with the first business rule an application ever gets
**Wants:** the row's owner checked against the caller, on every verb that touches it.
**Story:** They read this module, find the service seam and the before-hooks,
and write the check where the documentation put them. Then somebody deletes
somebody else's article.
**Must hold:**
1. The rule sees the row as stored, not the patch the client sent.
2. It runs on update, replace and delete, not only on the ones with a hook.
3. Reading a row you do not own and deleting it answer consistently.
4. The check and the write it guards cannot be separated by another writer.
**Today:** ❌ missing at this module's seams — and answered one subsystem over.
**Evidence:** none of the four is reachable from `port`. (1): the update hook is
handed the patch and the update command carries no loaded row
(`port/command.go:48-54`); an embedder cannot load one either, because
`DefaultService.repo` is unexported (`port/service.go:74`). (2): no delete hook
exists at all (H-PORT-03). (3): `WithScope` narrows reads only, and its own doc
comment states the outcome — a tenant scope makes `GET /{id}` on somebody else's
row a 404 while `DELETE /{id}` answers 200
(`crud/http/crudnet/options.go:91-96`), pinned rather than left to be discovered
at `crud/http/crudnet/write_edge_test.go:69` and `:114`. (4): a
load-then-check-then-write written at the service seam is three statements.
The answer is `security.Gate` on the repository
(`crud/decorators/security/security.go:126`): `Policy.Inspect` is called with the
row as it exists on updates and deletes and with the incoming row on creates
(`:89-92`), and the gate overrides `Save`, `Update`, `UpdateAll`, `Delete` and
`DeleteAll` (`:460`, `:580`, `:641`, `:677`, `:716`) — which is [[D-030]]'s
obligation working. [[UC-004]] and [[UC-020]] are the guarantees; the security
sweep owns them.
**If not ready:** Nothing to build. What is missing is a sentence: this module's
two seams are the wrong place for a row-level rule, and the gate is the right
one. A consumer who reads only `port` writes the rule at the seam this module
advertises and ships the hole, and neither `port/doc.go` nor
`docs/modules/en/port.md` names `security` once.

### H-PORT-05 — The same service value on every transport, including the one added later
**Who:** a platform engineer whose team is on Gin and whose next consumer wants gRPC
**Wants:** the rule written once, mounted four times.
**Story:** They mount the service on Fiber, Gin and `net/http` today. Six months
later they register the same value on a gRPC server and change nothing.
**Must hold:**
1. One value satisfies every binding's `Service` type.
2. The same request answers the same status and the same body on all four transports.
3. Adding a transport costs nothing beyond the mount line — for the rules that live on the service.
**Today:** 🟡 partial — 1 and 2 hold; 3 holds only for a rule written on the service.
**Evidence:** `port/service.go:19` — three type parameters, because the mapper
runs before the service. `test/portmount/grpcmount_test.go:112`
(`TestTheSameServiceMountsOnAllFourTransports`) records the *command* each of the
four transports handed over and compares them across all eight verbs; its own
comment records that putting `NarrowForCount` back into one transport makes it
fail naming the offender. `test/portmount/mount_test.go:340` is the three-binding
half, and `:395` (`TestTheServiceIsWhereTheRulesRan`) is the one that says where.
(3) stops holding the moment a consumer takes H-PORT-03's advice. Every other
rule seam this library offers is declared once per binding with that framework's
own request type — `func(*http.Request, *M) error`
(`crud/http/crudnet/options.go:102`), `func(fiber.Ctx, *M) error`
(`crudfiber/options.go:100`), `func(*gin.Context, *M) error`
(`crudgin/options.go:102`), `func(context.Context, *M) error`
(`crud/rpc/crudgrpc/options.go:91`) — and the same is true of `WithScope` and
`WithTransform`. The sibling sweep states it outright at
`docs/ai/usecases/modules/crudhttp/Crudhttp.md:192`.
**If not ready:** The one-value property is real and it is narrower than it
reads. A platform engineer told that a fourth transport asks for nothing new,
who then finds their admin-only-patch rule needs a fourth copy, has been told a
half-truth. One sentence beside the claim — *the service is written once; a hook,
a scope and a presenter are written once per binding* — costs nothing and is the
thing H-PORT-02's table exists to say.

### H-PORT-06 — The request body is not the model
**Who:** the author of a public API whose stored row has six columns the wire must not show
**Wants:** a body type of its own on the way in, and those columns gone on the way out.
**Story:** They declare an input struct, write one `Model` method, and mount with
the constructor that names a mapper. Then they look at what a `GET` answers.
**Must hold:**
1. The mapper runs before the service, so no business rule sees a wire type.
2. Choosing one costs none of the generated routes.
3. A resource mounted with `Serving` and the same resource mounted with `ServingFor(svc, port.Identity[M]())` answer byte-identically on every route, error paths included.
4. The response shape is theirs too, and it is one value on every transport like the mapper is.
**Today:** 🟡 partial — 1 to 3 hold; 4 has no seam in this module.
**Evidence:** (1)–(3): `port/mapper.go:14`, and `Identity` at `port/mapper.go:21`
— the no-mapper case is the identity mapper, so the four constructors converge on
`build` (`crud/http/crudnet/handler.go:123`) and there is no per-route nil check
to get wrong.
(4): `Mapper[In, M]` declares `Model(ctx, in In) (M, error)` and nothing else
(`port/mapper.go:14-16`). It is input-only, and every response writes the model
(`crud/http/crudnet/handler.go:469-475` writes `m` unless `opt.transform` is
set). The way out is `WithTransform(fn func(*http.Request, M) any)`
(`crud/http/crudnet/options.go:83`) — a *binding* option, so this actor also
loses H-PORT-05's one-value property and writes the presenter four times.
H-CRUDHTTP-04 owns the presenter and the page envelope and marks the pair
partial.
The stated limit worth keeping here: `PATCH` decodes straight into the update
DTO and the mapper does not cover it (`port/doc.go:49-52`). It costs nothing
today because the generated DTO and the generated input body derive their JSON
names by the same rule — `_examples/example/blog/vv_gen.go:20` and `:77` both
spell `authorID` — but a hand-written DTO beside a generated mapper can disagree
and nothing says so.
**If not ready:** For 4, per binding, per resource, and stated nowhere in `port`.
One sentence on `Mapper`'s doc comment — *this is the way in; the way out is the
binding's presenter* — stops a consumer expecting the symmetry the name
suggests.

### H-PORT-07 — The key is a uuid, and the client brought its own
**Who:** the author of a public API whose ids are uuids generated offline by the client
**Wants:** `/articles/{uuid}` to work, and a create to keep the key it was sent.
**Story:** They change the key type, mount, and send a uuid in the path. Later
they turn on the option that lets a create carry its own key, because their
clients generate ids while offline and replay them.
**Must hold:**
1. A key type that can spell itself as text survives the round trip: what the server put in a URL is what comes back off one.
2. A key that cannot be parsed is a 400 naming the key, not a 500.
3. With client ids allowed, the key the client sent is the key that is stored — and `PUT` to an unused key creates.
4. With client ids not allowed, the key is cleared wherever it arrives.
**Today:** ✅ ready
**Evidence:** `port/request.go:15` (`CoerceID`) and `:60` (`FormatID`), written
as inverses arm for arm — `encoding.TextUnmarshaler` first, `time.Time` before
that. `port/inbound_test.go:32`
(`TestAKeyThatWentOutAsTextComesBackTheSameKey`) is the round trip with its own
control. (2): `CoerceID` returns `BadRequestAs(errs.CodeInvalidID, …)` on every
arm, so a client that formatted a uuid with `fmt.Sprint` and sent `[16]byte`'s
Go syntax gets 400 rather than 500. (3) and (4): `port/service.go:60`
(`AllowClientID`) changes what `Sanitize` clears and it changes whether
`Replace` can create (`port/service.go:192`).
**If not ready:** — but see H-PORT-01(5): with `AllowClientID` on, `Replace`
creating is exactly the path that skips an overridden `Create`, and the two
options are the ones a public API turns on together.

### H-PORT-08 — The error body names the key the client sent
**Who:** a front-end engineer marking a field red on a duplicate-email response
**Wants:** the key their own form posted, and never a guess.
**Story:** They run the generator with `-adapter`. It writes the input struct,
the mapper and the inverse map. A duplicate key comes back naming a key from
that struct.
**Must hold:**
1. A path no declared hop owns is marked approximate rather than invented.
2. The same violation names the same key on every transport.
3. The declared map is checked against the model at start-up, so it cannot half-cover a resource.
4. The stronger mechanism does not give the worse answer than the weaker one.
**Today:** 🟡 partial — 1 to 3 hold; 4 does not, and the failure is invisible.
**Evidence:** (1)–(3): `port/pathmap.go:87` (`NewPathMap`) — total *and* exact, so
an entry for a `generated` column is refused as firmly as a missing one, with the
reason written out at `:98-103`; `port/pathmap_test.go:144`
(`TestAMapMissingAWritableColumnRefusesAtDeclaration`) and `:206`
(`TestTheMapMustMatchWhatARequestCanCarry`) pin both directions. The generator's
side of that coverage is H-CODEGEN-08 and H-CODEGEN-10's. (2):
`test/portmount/mount_test.go:488`
(`TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings`) and
`test/portmount/grpcmount_test.go:228` for the fourth — each with a control that
mounts the same resource *without* the map and asserts the client is handed the
model field name, so neither positive leg can pass vacuously.
(4) is `port`'s own and is the sharp one. `encoding/json` matches field names
case-insensitively, so a hand-written lowerCamel client that posts
`{"authorId": 3}` is accepted. The declared hop then answers `authorID`
(`_examples/example/blog/vv_gen.go:115`: `"AuthorID": port.At("authorID")`) — a
key that client never sent. The raw-body fallback would have answered `authorId`,
because it matches against the keys actually in the body
(`port/porthttp/bodyindex.go:20-28`, `:97-108`). **The stronger mechanism gives
the worse answer**, and only for a client whose spelling does not match the
generator's.
**If not ready:** The consumer either generates their client from the same struct
or discovers the spelling from a failing response. Which convention the generator
should emit is H-CODEGEN-10's, marked there as missing deliberately; what is
`port`'s is that a declared hop is trusted absolutely and has no way to notice it
named a key absent from the body it is describing. The honest close before the
tag is documentation, and the module page is where the correction has to land —
see H-PORT-09.

### H-PORT-09 — We wrote the path map by hand
**Who:** a team adopting the library on a resource the generator was never run on
**Wants:** the same error-body field names without generating anything.
**Story:** They copy the map out of the module page, list the four columns they
care about, and mount. A violation arrives on the fifth.
**Must hold:**
1. A map that misses a column fails loudly, or the violation says it was a guess.
2. What the module page teaches is what the generator produces.
3. A column the update DTO cannot reach fails at start-up, whether or not the adapter was generated.
**Today:** ❌ missing for 1; 2 is wrong in the page a consumer copies from; 3 holds and is undocumented here.
**Evidence:** (1): the hand-written map is `port.Fields`, and an undeclared head
**passes through unchanged** (`port/path.go:29-36`) — the violation is *not*
marked approximate, so a client is handed a model field name with nothing saying
it is one. That is the inverse of the generated map's honesty, and it is
deliberate: a declining hop poisons `errs.Chain` and would mark approximate a
path the raw-body index resolves today (`port/path.go:13-25`, `port/doc.go:56-62`).
Deliberate, and strictly worse for the consumer than the zero-config path
H-PORT-10 rates ✅ precisely for its honesty — because there the guess declines
and here the miss is silent.
(2): `docs/modules/en/port.md:117` declares `json:"authorId"` and `:161` declares
`"AuthorID": port.At("authorId")`; the Russian mirror repeats both at `:119` and
`:164`. The generator emits `port.At("authorID")`. Worse, `NewPathMap` validates
the map's *keys* against the schema and never its values
(`port/pathmap.go:87-113`), so a hand-written map copied from the page passes the
start-up check and then names a key the generated client never sends — H-PORT-08's
failure arriving through the documentation instead of the generator.
(3): `port.MustCoverUpdate` / `CoversUpdate` (`port/pathmap.go:175`, `:228`) is
the one start-up refusal that reaches *every* generator consumer: [[D-050]]
emits it whether or not `-adapter` is on, *"because it is the half that matters
to every consumer"* (`docs/ai/decisions/D-050-the-generated-adapter-is-total.md:14-18`),
pinned at `port/pathmap_test.go:243`. A consumer whose model gains a writable
column and whose process now panics at init has no row here telling them that is
the design.
**If not ready:** For 2, four lines in two module pages, and it is a correction
of an active error rather than an omission. For 1, either say in the page that a
hand-written map is partial by design and a missed column reaches the client as a
model field name, or give `Fields` an opt-in strict mode. For 3, one row in
`docs/modules/en/port.md`'s shared-helpers table saying what the panic means.

### H-PORT-10 — Day one: no generator, no mapper, and an error body anyway
**Who:** anybody, an hour in, mounting a repository straight onto a router
**Wants:** to see what a client gets before generating anything.
**Story:** They call the one-line constructor with a repository, mount it, post a
body with a duplicate email, and read the response.
**Must hold:**
1. Mounting with no mapper and no map adds no start-up work and no per-request allocation.
2. A field-level refusal still names a field, and if it is a guess it says so.
3. What it names is the key from the body when the body is JSON and was retained, and the model's own field name otherwise.
**Today:** ✅ ready, and the honesty in (2) is the point
**Evidence:** (1): `crud/http/crudnet/options.go:152` (`rendererFor`) returns the
one shared `defaultRenderer` (`:146`) when `port.Hops` returns nothing, which is
exactly the no-mapper case — the zero-config path allocates no renderer at all.
(2) and (3): `port/porthttp/bodyindex.go:49` (`BodyResolver`) indexes the retained
body's leaves and matches the model field name against them; it declines rather
than guessing, always (`:97-108`: an exact pointer wins, an index step declines,
and a name that folds to two leaves declines), and `port/violations.go:86` marks
the violation `Approximate` on a decline. Its three stated limits are JSON only,
a name that folds to two leaves declines, and a body that was retained but could
not be read still produces a *declining* hop rather than nil — because "I did not
translate this" and "there was nothing to translate" are different answers.
**If not ready:** — but the `Approximate` marker never reaches a client
(`errs/violation.go:73-82` strips it), so the consumer cannot see from the
response which of the two they got. That is [[D-044]] working as designed and
H-PORT-22(3) carries it.

### H-PORT-11 — A hand-written endpoint fails like its neighbours
**Who:** anybody, in week two, adding `POST /articles/{id}/publish`
**Wants:** the same envelope, the same status, the same bounded decode, without copying a renderer.
**Story:** They write an ordinary handler that returns an error, decode the body
through the library's decoder, and let the failure render itself.
**Must hold:**
1. Returning `crud.ErrForbidden` from a hand-written handler answers 403 with the same body a generated route answers, and the decode is bounded at the same number.
2. A refusal that names a field names the key the client sent, exactly as a generated route does.
3. The package that owns the envelope is enough — reaching it does not mean importing the CRUD subsystem.
**Today:** 🟡 partial
**Evidence:** (1) is `crud/http/crudnet`'s to hold, not `port`'s, and the sibling
sweep owns it as H-CRUDHTTP-10, where it holds for the hand-written half and
does not for the generated half — that is blocker 1. `crudnet.WithErrors(f, opts...)`
(`crud/http/crudnet/middleware.go:36-38`) is the one existing name that gives a
hand-written `net/http` handler the configured renderer in a single call, and it
is named in no sweep and no module page's install section.
(2) does not hold on two of three bindings. The retained body reaches the
renderer through a context (`port/porthttp/body.go:200`), and `crudnet.Errors`
renders with the `*http.Request` it passed *down* (`crud/http/crudnet/middleware.go:76-78`),
so a context a handler installed on its own copy is invisible — including through
`WithErrors`, which is `Errors(opts...)(f)`. On Fiber the bytes come out of an
*unexported* Locals key (`crud/http/crudfiber/options.go:199`, key declared at
`crud/http/crudfiber/handler.go:460`), which a hand-written handler cannot set at
all. Only Gin works, and only because the request pointer is shared.
(3) does not, in the narrow form that is true: `porthttp` exports the `Renderer`
(`port/porthttp/render.go:19`) and nothing that installs one. Three exported
functions write a rendered error to a response — `crudnet.DefaultErrorHandler`
(`crud/http/crudnet/options.go:168`), `crudgin.DefaultErrorHandler`
(`crud/http/crudgin/options.go:193`), `crudfiber.DefaultErrorHandler`
(`crud/http/crudfiber/options.go:188`) — and every one of them is hard-wired to
its binding's unconfigured `defaultRenderer`. The only exported writer that
*takes* a `Renderer` is `auth/http/authhttp/authhttp.go:67`, in the auth
subsystem.
**If not ready:** Today they get the catalogue or the field names, not both:
return the error and the middleware renders it with no body index, or render it
themselves through `DefaultErrorHandler` and get neither the catalogue nor the
hops. The fix lands in `port/porthttp` (a writer, and a decode that retains
without needing the caller to reinstall a context) and in `crud/http/crudfiber`
(export the body key, or take one).

### H-PORT-12 — The client hears the message in its own language
**Who:** the author of a product sold in France and Japan
**Wants:** one flat file per locale, loaded once, honoured everywhere.
**Story:** They embed the catalogue, build it, and install it the way the README
says to. They send `Accept-Language: fr` and expect French.
**Must hold:**
1. The locale is read from the request, not carried on the fault.
2. The ladder is keyed on the client's own path, not the model's field name.
3. Installing the catalogue once covers the generated routes, the hand-written ones and the 401 at the door.
4. Installing it on a generated resource does not cost that resource its generated path map.
**Today:** ❌ missing for (3) and (4)
**Evidence:** (1) and (2) hold and are pinned: `crud/http/crudnet/options.go:181`
reads the header at render time, `port/violations.go:72` applies the path chain and `:94-97`
expands the messages afterwards, and `port/violations_test.go:221`
(`TestTheMessageLadderSeesTheTranslatedPath`) fails if the order is swapped. The
ladder and the flat-file catalogue themselves are `errs`' — H-ERRS-05.
(3) does not. A generated route never returns its error to the middleware; it
renders itself through the renderer `build` closed over
(`crud/http/crudnet/handler.go:123-136`, `crudgin :119-132`, `crudfiber :114-127`,
`crud/rpc/crudgrpc/handler.go:78-83`) and sets the very marker each middleware
checks before rendering (`crud/http/crudnet/middleware.go:58`,
`crudgin/middleware.go:55`, `crudfiber/middleware.go:41`). The README and ten
module pages present that middleware as how the catalogue is installed.
(4) does not. `WithRenderer` replaces the renderer *before*
`rendererFor(port.Hops(svc, mapper))` is consulted
(`crud/http/crudnet/handler.go:129-131`, `crudgin :125-127`, `crudfiber :120-122`,
`crudgrpc :79-81`), so the one spelling that works on a generated resource
silently drops the map. Both usage guides teach exactly that spelling —
`docs/usage-guides/ent.md:899-900` and `docs/usage-guides/gorm.md:837-838`.
`WithErrorHandler` is a third way to lose it: `build` constructs a renderer only
when `errorHandler == nil` (`crud/http/crudnet/handler.go:125`), so the consumer
who wires an error handler to log a 500 — which is what H-PORT-20's *If not
ready* tells them to do — loses the path map as well.
**No test anywhere asserts that a catalogue installed on the middleware reaches
a generated route.** The one end-to-end locale test wires it per resource
(`crud/http/crudnet/options_test.go:517`), and the only middleware tests that
pass `WithMessages` pass a panicky source to exercise panic recovery
(`crud/http/crudnet/middleware_test.go:128` and its two siblings).
**If not ready:** They discover it when a French client gets English. The correct
wiring today is
`WithRenderer[M, ID, U](porthttp.NewRenderer(porthttp.WithResolvers(port.Hops(svc, mapper)...), porthttp.WithMessages(cat)))`
per resource, plus the options a second time on the auth middleware. It compiles
and it works; it is written down nowhere.

### H-PORT-13 — Ship RFC 9457 instead of the library's envelope
**Who:** a team whose API guidelines mandate `application/problem+json`
**Wants:** the status table and the violation pipeline, a different body.
**Story:** They implement the `Renderer` interface once and install it for the
whole server.
**Must hold:**
1. Replacing the body keeps the statuses, the codes, the message ladder and the path chain.
2. It is installed once, not once per resource.
3. It covers the failures other subsystems raise — a 401 comes out of the same shape.
**Today:** 🟡 partial — (1) is unverified, (2) and (3) do not hold.
**Evidence:** (1) is reachable by construction: `port/porthttp/render.go:19` is
the seam and `port.Violations` (`port/violations.go:55`) is the five steps with
no protocol in them, callable by anyone implementing it. It is **not pinned**.
`grep -rn ") Render(" --include="*_test.go" .` finds exactly one non-library
implementation in the repository — `auth/http/authhttp/refuse_test.go:37`
(`stubRenderer`, in the auth subsystem, returning a fixed triple). Every CRUD
`WithRenderer` test hands back the library's own `NewRenderer(...)`
(`crud/http/crudnet/options_test.go:523-524`, `crudgin :521-522`,
`crudfiber :521-522`, `crud/rpc/crudgrpc/handler_test.go:582`), which proves the
option is wired and not that the pipeline survives the swap.
(2) and (3): `WithRenderer` is a per-handler option spelling all three type
parameters (`crud/http/crudnet/options.go:77`, `crudgin/options.go:77`,
`crudfiber/options.go:75`, `crudgrpc/options.go:66`); the error middleware and
the auth middleware take `RenderOption`s only and build an `EnvelopeRenderer`
unconditionally (`crud/http/crudnet/middleware.go:52-55`,
`auth/http/authnet/authnet.go:43-47` via `authhttp.RendererFor`), so a
replacement renderer cannot be installed on either. `crudfiber.ErrorHandler`
(`crud/http/crudfiber/middleware.go:57`) is a fourth install seam with the same
limit, and `docs/modules/en/crudfiber.md:126-128` recommends it with the claim
that it *"covers everything the app serves"*.
**If not ready:** Twenty resources means twenty `WithRenderer[…]` lines, each of
which also drops that resource's path map (H-PORT-12(4)), and the 401 still
answers the library's envelope — a public API with two body shapes. A
`Renderer`-taking variant of the middleware constructors does *not* close this on
its own: by blocker 1 a generated route never reaches either middleware's
renderer. [[UC-015]] guarantee 9 promises replacement *per handler* and nothing
wider, so (2) is a feature request against that use case rather than a guarantee
that does not hold; (1)'s missing test is a gap in what the guarantee already
claims.

### H-PORT-14 — A 401 answers even when nothing else is installed
**Who:** the author of an API that mounts an auth middleware before it mounts anything else
**Wants:** an unauthenticated request to get a body, not an empty 200.
**Story:** They put the auth middleware in front of a mux, forget the error
middleware, and send a request with no token.
**Must hold:**
1. The refusal is written at the door and does not depend on a second middleware being installed.
**Today:** ✅ ready
**Evidence:** `auth/http/authhttp/authhttp.go:10-16` records the reasoning —
*"the failure mode of 'the door was open' must not depend on a second thing being
installed"* — and `:67` (`Refuse`) writes it, taking a `porthttp.Renderer` as a
parameter, which makes it the only exported writer in the repository that does.
**If not ready:** — The other two halves of this story — that installing both
renders once, and that a 401 uses the same envelope and status table as a 422 —
are `auth/http/authhttp`'s and are H-AUTHHTTP-06's. What that case cannot see is
that a catalogue installed on both middlewares still does not reach a generated
CRUD route; that is H-PORT-12(3).

### H-PORT-15 — Bound what a client may ask for, once, for every transport
**Who:** an author exposing a filter DSL to the public internet
**Wants:** one config that says what may be filtered, how many ids a bulk delete takes, how large a body may be.
**Story:** They write a `query.Config`, pass it to the service, and mount. They
cap the bulk delete on the handler because that one is the transport's business.
**Must hold:**
1. The same `MaxBody`, `MaxBulk`, `AllowClientID` and query config produce the same status and the same error code on every transport that has them.
2. An unset bulk cap means the same number on every transport.
3. Handing a service-shaped option to a constructor that was given a finished service fails at start-up, named, rather than being ignored.
4. `ReadOnly` answers the same way on every transport for a verb it removed.
**Today:** 🟡 partial — 1 to 3 hold, with one asymmetry written down; 4 does not.
**Evidence:** `port/rules.go:34` for the shared struct, `:61` for `BulkCap` — a
method rather than a defaulted field, *"so the four transports cannot disagree
about what an unset `MaxBulk` means, which is how they came to agree it meant no
cap at all"* — pinned as `TestMaxBulkCapsOneRequest` in four places
(`crud/http/crudnet/options_test.go:278`, `crudgin :280`, `crudfiber :281`,
`crud/rpc/crudgrpc/handler_test.go:499`), with the unset-cap case at
`crudnet/options_test.go:581` sending `DefaultMaxBulk+1`. The asymmetry in (1):
`MaxBody` reaches the three HTTP bindings only, because gRPC bounds a message at
the server before a handler runs; `port/rules.go:20-27` says so and [[FL-013]]
carries the row. `:91` (`RefuseServiceOptions`) is the start-up panic, pinned as
`TestAServiceShapedOptionOnServingIsRefusedAtDeclaration` in four places
(`crudnet/options_test.go:456`, `crudgin :454`, `crudfiber :454`,
`crud/rpc/crudgrpc/handler_test.go:445`).
(4) does not hold, and `ReadOnly` is the one field on the shared struct whose
effect is not shared. It is implemented as *do not register the route*
(`crud/http/crudnet/handler.go:161`, `:174`; `crudgin :148`, `:157`;
`crud/rpc/crudgrpc/service.go:70`), so the answer belongs to the router. Gin
answers 404 by default; the three `TestReadOnlyMountsOnlyTheReadRoutes` all assert
405 and the Gin one passes only because its fixture sets
`r.HandleMethodNotAllowed = true`, with the comment saying why
(`crud/http/crudgin/fake_test.go:251-258`). A client branching on 405 gets 404
from a Gin deployment. [[FL-013]]:105 records the row; no module page does, and
H-CRUDHTTP-01(5) owns the binding half.
**If not ready:** For 4, one line on the Gin page telling the consumer to set the
flag, and the [[FL-013]] row carried into the module pages.

### H-PORT-16 — We already have forty domain errors
**Who:** a team adopting the library into a service that has had a domain layer for two years
**Wants:** `ErrQuotaExceeded` to arrive as something a client can act on.
**Story:** They mount the routes, return their own sentinel from a service
override, and get a blank 500. They look for the place to register it.
**Must hold:**
1. A consumer's own sentinel can be classified without rewriting every return site.
2. A public API that rate-limits can answer 429 through the same contract as everything else.
3. A vocabulary of the consumer's own codes reaches the client.
**Today:** ❌ missing for 1 and 2; 3 holds.
**Evidence:** (1): `sentinelKind` is a closed switch over this library's own
sentinels and its default arm is `errs.KindInternal` (`port/kind.go:110-129`).
There is no registry, no `errors.As` hook, no SPI on the classification side. A
consumer's `ErrQuotaExceeded` is an opaque 500 wherever it is returned, and the
only ways in are to build an `errs.Fault` at every return site or to convert in
a wrapper the library never asks for.
(2): the kind table is closed at nine. `rank`'s default is `0` and `StatusFor`'s
default is 500 (`port/kind.go:96-99`, `port/porthttp/errors.go:69-70`), so a
consumer-defined `errs.Kind` value answers 500 — and `errs.Kind.String` makes the
same choice, so it is `internal` on the wire too (`errs/code.go:93-95`). The
constants are `errs/code.go:77-91` and the numeric values are documented as *"not
API"*, so the table is not extensible from outside and is not meant to be. A
public API that rate-limits cannot answer 429 through this contract.
(3) holds and is the half that is right: `errs.Codes` is the seam
(`porthttp.WithCodes`, `port/porthttp/render.go:51`), and a code carries the kind
it maps to — *"which is what lets a service declare fifty codes of its own
without touching a status table"* (`errs/code.go:63-65`).
**If not ready:** They wrap. Every domain error becomes
`errs.New(errs.KindConflict).Wrapping(ErrQuotaExceeded).Code("quota_exceeded").Fault()`
at the boundary, which is a function they write and a discipline they keep.
Closing 1 is a classification SPI on `errs.Codes` — the value is already threaded
through `KindOfWith` (`port/kind.go:32`) — or a documented recipe in
`docs/modules/en/port.md` for the wrapper. Closing 2 is a tenth kind, one row in
`StatusFor`, one in `KindForStatus`, one in `rank` and one in the gRPC table, and
after the tag it changes a body a mobile client is already parsing (H-PORT-22).
Before the tag it is cheap; after it is not.

### H-PORT-17 — A background worker runs the same rules with no request in sight
**Who:** the author of an importer that ingests a nightly CSV
**Wants:** the quota check, the clearing and the query bounds, without HTTP.
**Story:** They construct the commands as values and call the service. When a row
fails they turn the error into violations and log them.
**Must hold:**
1. A command carries no framework — nothing in it names a status, a header or a request type.
2. The same service answers a worker and a request identically.
3. The violation pipeline is reachable with no transport at all, and the locale can be set on a background context.
4. The list a worker gets is bounded the way every other list in this library is.
**Today:** 🟡 partial — 1 to 3 hold; 4 does not.
**Evidence:** `port/command.go:8-16` — *"what the request meant and nothing about
how it arrived"*; the whole file imports `crud` and `crud/query` and nothing
else. `port/doc.go:33-35` states the scope decision: a queue consumer calls a
`Service` directly and never builds a command. `port/violations.go:55` takes a
`context.Context` and a fault and nothing else; `:47-49` names the
background-context case. `port/service_test.go:155` drives the whole service
through a fake repository with no transport in the test at all.
(4): `ViolationOptions.Max` is *"Zero or less means no cap"*
(`port/violations.go:29-30`, enforced at `:91`), and the zero value is what a
worker constructing the struct by hand gets. It is the one caller in the library
with no cap: `MaxBody` defaults to 4 MiB, `DefaultMaxBulk` to 1024, and
`EnvelopeRenderer` starts at `MaxViolations` (`port/porthttp/render.go:80`).
A nightly importer logging violations per failed row is exactly where an
uncapped list bites.
**If not ready:** They pass `Max: port.MaxViolations` once they have read the
field comment. Closing it is either a documented default in `Violations` when
`Max` is zero — which changes an exported behaviour and needs a decision — or
one line in `docs/modules/en/port.md` saying the struct's zero value is the
uncapped one.

### H-PORT-18 — Forty thousand rows, through the same rules
**Who:** the same importer, on the first night the file is large
**Wants:** the quota check and the clearing on a batch, in as few statements as the library can manage.
**Story:** They have the service and the rules. They look for the command that
takes a slice, do not find one, and write a loop.
**Must hold:**
1. A create of many rows is expressible against the service.
2. It costs one statement, or as few as the repository can do, rather than one per row.
3. A route exists for it, or the module says plainly that batch import is not a transport concern.
**Today:** ❌ missing for all three
**Evidence:** there is no `CreateManyCommand`. `port/command.go` declares eight
commands and the only plural one is `BulkDeleteCommand` (`:72-74`).
`port.Repository` deliberately omits `SaveAll` — *"It is narrow on purpose: it
lists what the routes call, not what the repository can do. Every method added
here is a method every hand-written stand-in has to supply"*
(`port/repository.go:13-25`) — while `crud.Repo.SaveAll` exists and is the
one-statement write the library advertises (`crud/repo.go:41-43`, [[UC-008]]).
So adopting the service seam for its rules costs the batch write, and there is no
route for it either: the ten HTTP routes are nine reads and writes plus one
`POST /bulk-delete` (`crud/http/crudnet/handler.go:14-23`).
**If not ready:** They loop, and the quota check plus the clearing run forty
thousand times against forty thousand statements. The escape is to hold the
`crud.Repo` beside the service and call `SaveAll` on it — which walks past every
rule the service exists to hold, and is exactly the shape H-PORT-01(7) warns
about. Closing it is either a ninth command and a ninth `Repository` method — a
method every hand-written stand-in then supplies, which is the reason it was left
out — or a sentence in `port/doc.go` saying batch import is the repository's and
naming `crud.Repo.SaveAll`. The second costs nothing and is a decision the owner
should make before the tag, not one a consumer discovers at 40,000 statements.

### H-PORT-19 — Test my own service without a database or a router
**Who:** anyone who has moved a quota rule onto the service seam
**Wants:** a unit test over the rule, with no container and no HTTP.
**Story:** They write a test, look for a stand-in for the repository, and either
find one or write one.
**Must hold:**
1. Faking the layer below the service is cheap — small enough to write inline.
2. Faking the layer above is unnecessary: the service is callable with a plain value.
3. What the test asserts is what a request would have produced, not what the test itself set up.
**Today:** 🟡 partial — 2 holds; 1 costs eight methods; 3 has no shipped support.
**Evidence:** (2) is the point of the commands: `port/service_test.go:155`
constructs commands as values and drives all eight verbs with no transport.
(1): the stand-in is `port.Repository`, eight methods
(`port/repository.go:16-25`), and the library writes its own each time —
`fakeRepo` at `port/service_test.go:53`. Nothing exports one.
`crud/crudtest` is a `crud.Source` — it records SQL and replays result sets
(`crud/crudtest/recorder.go:1-5`) — so it is a stand-in for the *driver*, not for
the repository, and using it to test a service means building a real repository
over it. The one type in the tree that satisfies `port.Repository` and is meant
to be substituted is `remote.Resource`, one subsystem over.
(3): sibling sweeps carry this case and answer it —
`docs/ai/usecases/modules/remote/Remote.md:618` (H-REMOTE-12, *"Test my service
without the other one running"*), where the fakeable interface is one method.
Nothing equivalent is documented for `port`.
**If not ready:** They write the eight methods, once, and most of them return
zero values. Closing it is a `porttest.Repo[M, ID, U]` — an in-memory
`port.Repository` with the same shape `port/service_test.go` already has — or
one section in `docs/modules/en/port.md` showing the fake. Either is small, and
this is the first question anybody asks within an hour of taking H-PORT-01's
advice.

### H-PORT-20 — Why was that a 500, where did the log line go, and what happened to the client that hung up
**Who:** whoever is on call, and whoever owns the error-rate alert
**Wants:** the library's own lines in the application's logger, the cause of a 500 reachable, and a cancelled request not counted as one.
**Story:** They put a `*slog.Logger` into the request context and expect the
library's lines to carry the trace id. An endpoint starts answering 500; the body
says nothing, correctly; they go to the logs and find nothing about it.
Separately, the alert fires because a load balancer's client disconnects are all
500s.
**Must hold:**
1. Nothing writes to a process-wide logger from library code, and a request whose context carries no logger still gets its lines somewhere.
2. When the renderer decides an error is internal, the cause is still in scope for somebody who wants it.
3. A client that hung up, and a query that blew its deadline, are not the same thing as a bug in the server.
4. Whatever the answer to 2 is, it is one answer for the process — not one per resource.
**Today:** 🟡 partial — 1 holds; 2, 3 and 4 do not.
**Evidence:** (1) holds and is the seam [[D-062]] describes: `port/log.go:26`
(`Logger`, never nil) and `:41` (`WithLogger` ignores nil rather than storing
it), pinned at `port/log_test.go:21`
(`TestTheLibrarysOwnLinesGoWhereTheApplicationSays`). There are nine call sites
and every one is a panic recovery or an encode failure —
`crud/http/crudnet/middleware.go:69`, `crud/http/crudnet/options.go:212`, and
seven more, all found with `grep -rn "port.Logger("`.
That completeness is the problem. (2): `port/porthttp/render.go:129-131`
short-circuits the internal case and returns `Internal()`; the original error
never leaves the function, and the `Renderer` contract —
`Render(ctx, err) (int, http.Header, any)` — gives a consumer no seam to observe
what it is hiding. So the *only* place in the process where the cause of a 500 is
still in scope is inside a custom `Renderer`, which today can only be installed
per resource (H-PORT-13(2)) — which is (4) failing for the same reason.
(3): `port/kind.go:126-127`'s default arm is `errs.KindInternal`, and
`grep -rn "context.Canceled\|DeadlineExceeded" --include="*.go" .` excluding
tests comes back **completely empty** — nothing anywhere in the repository
classifies either. A service behind a load balancer sees client disconnects as
ordinary traffic; turning them into 500s with an empty body is how an error-rate
alert becomes noise nobody reads, which is what makes the real 500 invisible.
The binding half of 2 and 4 is H-CRUDHTTP-14, which reaches the same two lines
from the other side and notes that only Gin keeps the cause, through `c.Error(err)`
(`crud/http/crudgin/options.go:201`).
**If not ready:** The consumer writes a per-resource, per-binding
`WithErrorHandler` that logs and then calls `DefaultErrorHandler` — which costs
them the hop-aware renderer, because setting an error handler skips `build`'s
renderer entirely (`crud/http/crudnet/handler.go:125`). Closing 3 is two arms in
`sentinelKind`; closing 2 and 4 is one render option, and the DX section proposes
it.

### H-PORT-21 — A gateway reads a failure back and keeps the branch it already wrote
**Who:** the author of a BFF calling an internal CRUD service
**Wants:** `errors.Is(err, crud.ErrNotFound)` to keep working across the network.
**Story:** They call the service, get a 404 with an envelope, and branch on the
same sentinel they used when the repository was in-process. A misconfigured base
URL must not read as an empty table.
**Must hold:**
1. The status becomes the class, and the class plus the code rebuild the fault — sentinel included.
2. A finer code survives: a stale version is still distinguishable from any other conflict.
3. A body that is not one of this library's envelopes is refused, not decoded optimistically.
**Today:** 🟡 partial — all three behave correctly; (3) has no test in the package
that owns it.
**Evidence:** (1) and (2): `port/kind.go:250` (`FaultFrom`) puts the sentinel
back and `:292-296` keeps `ErrStaleVersion` distinct from `ErrConflict` — which
is why `sentinelFor` takes the code as well as the kind;
`port/inbound_test.go:65`, `:103`, `:135`, `:148` pin the reconstruction half.
`port/porthttp/decode.go:22` (`KindForStatus`) inverts
`port/porthttp/errors.go:51` (`StatusFor`) — the same package, not the same
file, and the doc comment's *"beside the table it inverts"* means the package.
(3): `port/porthttp/decode.go:57` returns `false` for a body whose `type` is not
`"error"`, with the reason written out — a router's own plain-text 404 would
otherwise become `crud.ErrNotFound` permanently and quietly. But
`grep -rn "ParseEnvelope" --include="*_test.go" .` **returns nothing**: there is
no `decode_test.go` in `port/porthttp`, and the property is held end to end one
module away, by `remote/roundtrip_test.go:333`
(`TestARouters404IsNotAMissingRow`), whose harder leg at `:354-367` sends a
gateway's *JSON* 404 — the case a JSON-parse check alone would miss. `remote` is
the only in-repository caller (`remote/remotehttp/transport.go:248`; the third
hit is the forwarder at `crud/http/crudhttp/porthttp.go:87`), and the surface is
exported, so a consumer's own gateway is the second one. The consumer story is
`remote`'s and H-REMOTE-05 owns it; what is `porthttp`'s is the table pair and
the parse.
**If not ready:** Nothing to write by hand. Closing it is a `decode_test.go` in
`port/porthttp` with four bodies — an envelope, a plain-text 404, a gateway's
JSON, and an envelope whose `type` is missing.

### H-PORT-22 — The mobile client that renders its own wording
**Who:** the author of an iOS app on a two-week app-store cycle
**Wants:** the code and the numbers, and to do the sentence itself — and to know which of it is safe to hard-code.
**Story:** They read `error_code: "too_young"` off the envelope and want to
render "must be at least 18" without shipping 18 in the binary. Then they ask
which parts of the body will still be there after the tag.
**Must hold:**
1. A violation's code reaches the client.
2. The parameters that code's message template consumes reach the client too.
3. A client can tell a field name that was declared from one that was guessed.
4. What the wire shape promises at the tag is written down.
**Today:** ❌ missing for 2, 3 and 4; 2 and 3 by design.
**Evidence:** `errs/violation.go:73-82` — `MarshalJSON` emits `field`,
`error_code` and `message` and nothing else, on a *value* receiver so a violation
marshalled as a map entry or a struct field cannot bypass it. The comment says
why in two places: `Params` *"stays server-side. Rendering it would put an
internal name one interpolation away from a response body, which is the quiet
half of [[D-044]]"* (`errs/violation.go:60-63`), and `Approximate` is *"a marker
for a consumer's own logic [and] not a thing to hand a client"*. So the client
gets `too_young` and the server-rendered sentence, and has to hard-code 18 to say
anything else; and it cannot tell H-PORT-10's declining guess from H-PORT-08's
declared hop.
(4): `docs/api/surface.md` baselines the *Go* surface. Nothing states which of
`error_code`'s strings, the `type: "error"` marker
(`port/porthttp/decode.go:55-56`) and the validation/general grouping
(`port/porthttp/render.go:140`) a client may branch on across versions — which
matters most in the module that owns the envelope, and more so because this file
proposes changes (a tenth kind for 429, an observer, a params option) that would
move two of the three.
**If not ready:** The escape hatch exists and is per-resource: `port.Violations`
hands `Params` and `Approximate` back as Go fields, so a custom `Renderer` can
emit them — and then owns [[D-044]]'s risk, which is the point of making it a
decision. A consumer needs to learn this *before* they build the client, and the
only place it is currently written is a struct-field comment. Closing 2 and 3 is
a paragraph in `docs/modules/en/porthttp.md`, or a `WithParams()` render option
that makes the trade explicit and revocable — which is a change to [[D-044]] and
needs the owner, not a pull request. Closing 4 is a short "what is stable"
section on the same page, and it is the cheapest of the three.

## The DX this should have

### The call site

```go
// one service, bounded; one error contract for the whole process
svc := port.NewService(articles, port.WithQuery(cfg))

fail := porthttp.Errors(
    porthttp.WithCodes(codes),
    porthttp.WithMessages(cat),
    porthttp.WithObserver(logCause), // sees the 500 before its body is emptied
)

mux := http.NewServeMux()
crudnet.Serving(svc).Mount(mux, "/articles")
mux.Handle("POST /articles/{id}/publish", porthttp.Handle(publish))

http.ListenAndServe(":8080", fail(authnet.Middleware(guard)(mux)))
```

```go
func publish(w http.ResponseWriter, r *http.Request) error {
    var in PublishInput
    if err := porthttp.Bind(r, &in); err != nil { // bounded, and the body is kept
        return err
    }
    a, err := publishArticle(r.Context(), r.PathValue("id"), in)
    if err != nil {
        return err
    }
    return porthttp.JSON(w, http.StatusOK, a)
}
```

The zero-config path is `crudnet.New(repo).Mount(mux, "/articles")` and nothing
below adds to it.

**This is two deliverables and they should be priced apart.** Only the first is
what a tag needs.

#### Deliverable A — the install seam. Three new names.

1. **A generated handler hands its error up when the middleware's recorder is
   present.** The shape already exists and is already the right one:
   `crudnet.HandlerFunc.ServeHTTP` checks `if rec, ok := w.(*recorder); ok` at
   `crud/http/crudnet/middleware.go:27-30` and files the error instead of
   rendering. The generated routes' `fail` does the same; on Gin it is
   `c.Error(err)` without writing, on Fiber it is returning the error, on gRPC it
   is returning the error unwrapped by a status. With no middleware installed
   nothing changes — the handler renders itself, which is what keeps the
   zero-config case free, and `authhttp.Refuse` takes the same branch so
   H-PORT-14(1) survives intact.

   **The precedence rule has to be stated, or this recreates blocker 2 inside
   the fix:** a resource that was given its own `WithRenderer` or
   `WithErrorHandler` keeps rendering itself and never defers. Only a resource
   with no renderer of its own hands the error up. Otherwise mounting the
   middleware silently turns off a renderer somebody configured per resource.

   **The ordering rule follows and has to be said out loud:** `fail` must be
   *outside* the auth middleware. `authnet.Middleware(guard)(fail(mux))` gives a
   silently English 401 while the 422 speaks French. That is a wiring order
   whose only symptom is a wrong-language body, and it is a real exception to
   [[D-021]] — named below rather than glossed.

2. **The declared hops travel on the request context, and `port.Violations`
   reads them itself.** `port.WithHops(ctx, hops)` / `port.HopsFrom(ctx)`, named
   for the `port.Hops(svc, mapper)` that produces the value — not
   `WithResolvers`, which `porthttp` already exports as a render option
   (`port/porthttp/render.go:64`) and would collide with in the one proposal
   whose subject is which of the two to reach for. `Violations` prepends
   `HopsFrom(ctx)` to `o.Resolvers` inside the pipeline, exactly as `Fallback` is
   already sourced from the context at `port/porthttp/render.go:135`.

   That placement is the whole point. **No renderer — the library's, or a
   consumer's `problem{}` — can then drop a declared hop**, because none of them
   is the thing that applies it. [[D-043]] becomes a property library code holds
   rather than an obligation a consumer inherits. `EnvelopeRenderer` keeps its
   `resolvers` field and `porthttp.WithResolvers` keeps meaning what it means, so
   nothing leaves the exported surface and no forwarder starts lying.

   The value is computed once in `build` and installed per request. `build` runs
   at wiring time and has no request (`crud/http/crudnet/handler.go:123-136`), so
   the installation is a wrapper each route method applies — crudnet already has
   that shape at `handler.go:490` (`r.WithContext(crudhttp.WithBody(...))`) and
   Fiber already writes to `c.Context()` at `options.go:199`. It has to live in
   the route methods and not in `Mount`, because `crudnet`'s package doc tells
   chi and gorilla users to register the ten methods themselves
   (`crud/http/crudnet/handler.go:29-31`) and those handlers pass through no
   `Mount`.

3. **`porthttp.WithObserver(func(context.Context, error))`**, a `RenderOption`
   called from `EnvelopeRenderer.Render` at the internal short-circuit
   (`port/porthttp/render.go:129`) before `Internal()` is returned. It fires
   wherever *any* `EnvelopeRenderer` runs — the middleware's, `build`'s and a
   per-resource one — so it closes H-PORT-20(2) and (4) on its own and does not
   wait on change 1. The context is the request's, so `port.Logger(ctx)` carries
   the trace id (`crud/http/crudnet/options.go:174-182` already derives the
   render context from the request). It is the envelope renderer's and dies if a
   consumer replaces the renderer, and that costs nothing: a consumer who wrote
   the renderer is holding the error already.

4. **Two arms in `sentinelKind`** for `context.Canceled` and
   `context.DeadlineExceeded`, which closes H-PORT-20(3) and needs a kind
   decision only if the answer is not `KindRetryable`.

New exported names: `port.WithHops`, `port.HopsFrom`, `porthttp.WithObserver`.

#### Deliverable B — the hand-written-endpoint toolkit. Nine more, and it can ship after the tag.

`porthttp.Errors(opts ...RenderOption)`,
`porthttp.ErrorsWith(rd Renderer, opts ...RenderOption)`, `porthttp.Handle`,
`porthttp.Bind`, `porthttp.JSON`, `porthttp.WithMaxBody`, plus
`crudgin.ErrorsWith`, `crudfiber.ErrorsWith` and `crudgrpc.ErrorsWith`.

Two constructors and not one positional-nil. `Errors(problem{}, WithMessages(cat))`
would compile and silently ignore the catalogue, because the options build an
`EnvelopeRenderer` nobody uses — the same silent-drop this document calls a
blocker two pages earlier. The name carries the distinction instead: *also use
this catalogue* and *replace the body* are different functions, `RenderOption`
keeps meaning exactly what it means today, and nobody writes `nil` to get the
default.

`Bind` returns only an error. `porthttp.Handle` installs an empty body holder on
the request context and `Bind` fills it, so there is no request to thread back
and no `_ = porthttp.WithBody(...)` to forget. That is what lets Fiber and
`net/http` supply the retained body from a hand-written handler, which is the
blocker-9 half nothing else here reaches. A hand-written route that is *not*
wrapped in `Handle` — a chi route beside a generated mount — still needs the
hops, and `porthttp.Handle` is the only place to put them; that half of
H-PORT-11 stays open until the toolkit lands, and change 1 does not close it.

### Turning one knob

```go
// a French catalogue on one resource, on top of the hops the generator wired
crudnet.ServingFor(svc, gen.ArticleMapper{}).
    Rendering(porthttp.WithMessages(fr)).
    Mount(mux, "/articles")
```

```go
// this subtree answers problem+json; everything else keeps the library envelope
mux.Handle("/imports/", porthttp.ErrorsWith(problem{})(imports))
```

The first is the one that matters. **Options compose onto what the binding
already wired; a `Renderer` replaces it** — and `Rendering` is a method on the
constructed handler rather than a free generic option, because the type
parameters are already bound there and nothing has to be spelled.
`crudnet.WithRenderOptions[Article, int64, ArticleUpdate](…)` would cost the same
three explicit type arguments as the `WithRenderer[M, ID, U]` line the DX table
scores `large`, and `crud/http/crudnet/options.go:26-48` already documents that
the mitigation is a hand-written helper per resource — twenty helpers on the
twentieth resource.

The second needs the precedence rule spelled or it is inert: today an inner
`Errors` sees the outer recorder and calls `next` without installing anything
(`crud/http/crudnet/middleware.go:57-61`), so the problem+json renderer would
never run. The change is to have the recorder carry the renderer it was installed
with, so an inner install replaces it for that subtree — innermost wins, which is
the only rule a reader guesses. It needs a test, and it is a behaviour change to
the double-install guard, so it belongs with deliverable B and not with the
blocker fix.

`porthttp.WithMaxBody(32<<20)` raises the decode cap and **not** `MaxKeptBody`,
which is 64 KiB (`port/porthttp/body.go:45-49`, `:101-108`). A resource
configured for large bodies therefore has every validation error marked
approximate. Either the doc says so where the knob is, or `WithMaxBody` raises
both — and raising both is a per-request memory decision, not a documentation
one. The name is `WithMaxBody` and not `Limit` so that `grep MaxBody` finds every
cap in an application; there are already two words for this number
(`port/porthttp/body.go:43`, `:70`) and a third would be one too many.

### Why this shape

Because the thing a consumer configures — a vocabulary, a catalogue, a body
format — is a property of the *process*, not of a resource. There are twenty
resources and one error contract, and any shape that makes the consumer repeat
the contract twenty times will have nineteen chances to drift and will take at
least one of them.

The alternative is what a hand-rolled server does: a per-handler renderer, wired
where the handler is. It costs one line per resource and it costs the whole path
chain, because the hops that translate a violation are known to the binding and
not to the consumer. That is the trade the current code makes without saying so,
and both usage guides teach it.

Putting the hops on the context rather than in the renderer is what makes the
process-wide install *possible* rather than merely convenient: a renderer built
once cannot know a per-resource mapper, and any design that keeps them in the
renderer is a design where one renderer cannot serve twenty resources.

Nothing here is generic — `port.WithHops`, `porthttp.WithObserver` and the two
`Errors` constructors name no type parameter — except `Rendering`, which is a
method on a value whose parameters are already bound. That matters because the
ceremony this proposal is trying to remove *is* the three-type-parameter line.

**One renderer for the process means one value handed to four constructors, not
one call.** `crudgin.Errors` returns `gin.HandlerFunc` and `crudfiber.Errors`
returns `fiber.Handler`; `porthttp` is `net/http` and [[D-036]] forbids it a
third-party requirement, so neither can forward to it. Each binding gains an
`ErrorsWith` of its own shape and only `crudnet.Errors` forwards. Four
constructors is still four against twenty-one call sites.

### What it must not break

- **[[D-045]]** — the shared half is transport-neutral. `port.WithHops` is
  `context` plus `errs.Resolver` and has no `net/http` in it, so it belongs in
  `port`; `Errors`, `Handle`, `Bind` and `JSON` are `net/http` and stay in
  `porthttp`. Its forbid list also says *"Do not break a binding's exported
  surface while moving. Alias, as D-034 did."* — `crudnet.Errors` keeps its exact
  signature and forwards; `crudgin.Errors` and `crudfiber.Errors` keep theirs and
  gain a sibling rather than a forwarder, because the alias trick cannot cross a
  framework's handler type.
- **[[D-059]]** — the HTTP projection of the error contract belongs to `port`.
  This is the decision the proposal *finishes*: the renderer moved so an auth
  middleware would not import the CRUD binding, and the middleware that installs
  it stayed behind. Its forbid list says the forwarders survive the first
  deprecation cycle; nothing here removes one, and `porthttp.WithResolvers` stays
  meaningful rather than becoming a no-op that lies.
- **[[D-043]]** — a path is translated one hop per layer, and a guess never
  overturns a declaration. Under change 2 the declared hops are applied by
  `port.Violations` itself, ahead of the raw-body fallback, so the property no
  longer depends on which renderer ran. **The one behaviour a consumer could trip
  on:** once the hops travel on the context, `WithResolvers(port.Hops(svc, mapper))`
  written by hand applies them twice, and a `PathMap` asked to resolve a head it
  already rewrote declines and marks the violation approximate. Almost nobody has
  that line, because no guide ever taught it — but `port.Hops`'s doc comment has
  to say it, and so does the H-PORT-12 recovery paragraph.
- **[[D-063]]** — every body a transport reads is bounded. `Bind` has no
  spelling for unbounded, `WithMaxBody` is a number and not a switch, and `JSON`
  marshals before it touches the header, which is that decision's other half.
- **[[D-021]]** — the magic fails at build or start-up. The renderer is a value
  at wiring time and the hops are installed by the binding. **The honest
  exception:** the middleware ordering above fails at request time and its only
  symptom is a 401 in the wrong language. Either a composition helper fixes the
  order — `porthttp.Chain(fail, authnet.Middleware(guard), mux)` — or the doc
  names the symptom. Claiming nothing can go quiet would be false.
- **The double-install marker stays the response-writer wrapper** and never
  anything on the error, because a fault is a value two goroutines may render at
  once. **Note for the owner:** every comment in the tree that states this cites
  [[D-042]] for it (`crud/http/crudnet/middleware.go:50`,
  `crud/decorators/faults/faults.go:81`, `crud/probe/full.go:46`,
  `port/violations.go:52`, and four more), and D-042 is *"The probe is advisory;
  the index is the truth"* — its invariant says nothing about faults or
  concurrency. Either the property needs a decision of its own or those citations
  need correcting; a reader who follows the link today finds nothing.
- **Challenged: [[D-045]]'s "a binding owns exactly three things", and
  `porthttp.JSON`.** That sentence lists *how a response is written* as the
  binding's (`docs/ai/decisions/D-045-...md:31-33`), and `JSON` writes a success
  body from the package that owns the error contract. The argument in favour is
  [[D-048]]'s: the marshal-first rule is implemented four times today —
  `crud/http/crudnet/options.go:204`, `crudgin/options.go:170`,
  `crudfiber/options.go:170` and inline at `auth/http/authhttp/authhttp.go:79` —
  and a count of four is what D-048 uses to decide something belongs in one
  place. The argument against is that a hand-written handler could write six
  lines of `json.Marshal` instead. Naming it, not deciding it.
- **Also for the owner, not for a pull request:** [[UC-015]] guarantee 9 reads
  *"The whole mapping is replaceable, per handler, without giving up the
  routes"*, and its **Out of scope** offers replacing the renderer as the way to
  problem+json with no claim about where it is installed. Of the blockers below,
  only 2 argues with a guarantee — replacement that costs the field names the
  same use case's guarantee 11 promises. Blocker 1 is about what the README and
  ten module pages say, and blocker 4 is a feature request for a wider install
  than any guarantee makes. `docs/ai/usecases/Index.md:86` lists UC-015 **covered**
  with no Gaps entry; it needs one, and one is enough.

## DX verdict
| What the ideal asks for | Today | Distance |
|---|---|---|
| `port.NewService(repo, port.WithQuery(cfg))` | exactly that, one line | none |
| embed and override one method | exactly that, no forwarding methods — but the default service calls its repository and never itself, so `Replace`, `DeleteMany` and `PUT`'s existence check all go past your override | small |
| a rule that can read the request | `BeforeSave` / `BeforeUpdate`, one line each; the error type it must return is documented in a use case and on no option, no doc comment and no module page | small |
| a rule on delete | nothing. Override `Delete` — and `DeleteMany` goes past it | large |
| a row-ownership rule | not here. `security.Gate` on the repository, which this module never names | small (a sentence) |
| one value on every transport | true of the service; a hook, a scope and a presenter are written once per binding | small |
| a wire type of its own on the way in | one `Model` method plus `ServingFor` | none |
| a wire type of its own on the way out | `WithTransform`, per binding, written four times; `Mapper` is input-only | large |
| a uuid or a `TextUnmarshaler` key | exactly that, no extra code | none |
| the generated map, total, refusing at start-up | exactly that | none |
| a hand-written map that fails loudly when it misses a column | not available: `port.Fields` passes an undeclared head through and does not mark it approximate | small |
| a transport scope that narrows an update | not available. `UpdateCommand` carries no `Options` while `List`, `Count` and `Get` do, and `Repository.Update` accepts them | small |
| write many rows in one request | nothing. No create-many command, no `SaveAll` on `port.Repository`, no route | large |
| classify my own domain errors | nothing. `sentinelKind` is closed and its default is 500 — build an `errs.Fault` at every return site, or wrap at a boundary you write | large |
| answer 429 | not available. The kind table is closed at nine and a kind from outside it is 500 | small before the tag, large after |
| a fake repository for a unit test | write eight methods. `crudtest` is a driver stand-in, not a repository one | small |
| bounded decode that keeps the body | `porthttp.DecodeJSONKeep` plus `porthttp.WithBody` plus a request you must not drop — and on Fiber it cannot be done at all. `porthttp.DecodeJSON` is the shorter name, is listed first, and silently costs the field names | large |
| one renderer for the whole process | not available. `crudnet.Errors(opts…)` covers hand-written routes only; every generated resource needs `WithRenderer[M, ID, U](porthttp.NewRenderer(opts…))`, and the auth middleware needs the options a third time | large |
| a catalogue that does not cost the path map | not available without `porthttp.WithResolvers(port.Hops(svc, mapper)...)` spelled by hand on every resource — one more call and two names no guide mentions | large |
| replace the envelope wholesale | per resource only; the 401 keeps the library's shape; no test installs a third-party renderer anywhere | large |
| an error-returning handler shape | `crudnet.HandlerFunc` and `crudnet.WithErrors` — in the CRUD binding, not in `porthttp`, and on `net/http` only | small |
| write a rendered error to a `ResponseWriter` | three `DefaultErrorHandler`s, each hard-wired to its binding's unconfigured renderer; the only one that takes a `Renderer` is `authhttp.Refuse`, in the auth subsystem | small |
| the cause of a 500, once, for the process | nothing. Only Gin keeps it, via `c.Error` | large |
| what the wire shape promises after the tag | nothing. `docs/api/surface.md` baselines Go, not `error_code` or `type: "error"` | small |

**Overall:** The half of this module with no transport in it feels finished — the
commands are values, the service is one interface, the path chain is one hop per
layer with the generated map refusing to boot when it stops covering the model,
and the classification is a single ordered switch that four transports read. The
error-side `large` rows are not five problems: they are one — **the install seam
is per-resource and the process-wide seam does not reach a generated route** —
seen from the catalogue, the path map, the body format, the retained body and the
500, and deliverable A closes all five. The rule-side `large` rows are a
different story and a worse one, because they are not one problem: there is no
seam for a delete, no seam for a batch, no seam for a domain error, and no
sentence anywhere telling a consumer which of the five seams reaches what. A
reader of this module today can write a quota check in one line and cannot write
*only the author may delete this* at all. Counted honestly: a catalogue that
keeps the path map across twenty resources is 20 sites of a
three-type-parameter `WithRenderer` line plus a 21st for the auth middleware, 5
names to know and a 6th on a resource with no mapper; deliverable A is three new
names and nothing per resource, deliverable B is nine more and one of them
(`JSON`) argues with a decision. The zero-config path is unchanged either way:
`crudnet.New(repo).Mount(mux, "/articles")` is one line and two names, and
nothing here adds to it. Customising does not extend the short path today; it
leaves it.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | The error middleware configures nothing on a generated route, on all four transports: the handler renders itself through a renderer `build` closed over and sets the very marker the middleware checks (`crud/http/crudnet/handler.go:123-136`, `crudgin :119-132`, `crudfiber :114-127`, `crudgrpc :78-83`). Four install seams say otherwise — `crudnet.Errors`, `crudgin.Errors`, `crudfiber.Errors`, `crudfiber.ErrorHandler` — and so do eleven documents (`README.md:1161`, `docs/modules/en/porthttp.md:136-141`, `crudnet.md:132`, `crudgin.md:116`, `crudfiber.md:132`, `errs.md:366`, and the five `docs/modules/ru/` mirrors). No test asserts a catalogue on the middleware reaches a generated route. The one place the mechanism *is* disclosed is `crudnet.Errors`' godoc (`crud/http/crudnet/middleware.go:44-46`), which over-promises in its first clause and states the truth in its second | blocker | A consumer following the README ships English default messages on every generated route with no signal. The tag ships the README, the module pages and the hover text |
| 2 | `WithRenderer` — the only per-resource way to install a catalogue — replaces the renderer before `port.Hops(svc, mapper)` is consulted (`crud/http/crudnet/handler.go:129-131`, `crudgin :125-127`, `crudfiber :120-122`, `crudgrpc :79-81`), so a resource that gains a catalogue silently loses its generated path map and starts answering model field names marked approximate. **Both usage guides teach exactly that call** (`docs/usage-guides/ent.md:899-900`, `gorm.md:837-838`) | blocker | Two advertised features cannot both be on at once, the one that turns off does so quietly in the error body, and the guides are what a consumer copies. Against [[D-043]] and [[D-050]], and against [[UC-015]] guarantees 9 and 11 read together |
| 3 | There is no request-aware seam on delete or bulk delete. `DeleteCommand` and `BulkDeleteCommand` carry ids only (`port/command.go:66-74`), `grep -rn "BeforeDelete"` is empty, `WithScope` is reads-only and says so (`crud/http/crudnet/options.go:91-96`), and an overridden `Delete` is walked past by `DeleteMany` (`port/service.go:234`) | serious | "Only an admin may delete this" is the second business rule every application gets, and the module's own seams cannot express it. The answer is in another subsystem and this module never names it |
| 4 | `porthttp` exports a `Renderer` and nothing that installs one process-wide. The three `DefaultErrorHandler`s are hard-wired to unconfigured renderers; the only exported writer that takes a `Renderer` is `auth/http/authhttp/authhttp.go:67`. Neither middleware constructor accepts a `Renderer`, only `RenderOption`s — and no test in the repository installs a third-party `Renderer` on a CRUD resource at all | serious | [[D-059]] moved the renderer out of the CRUD subsystem so an auth middleware would not import the repository; a hand-written endpoint still has to. RFC 9457 cannot be installed anywhere but per resource, and that it works per resource is unpinned |
| 5 | A consumer's own sentinel cannot be classified. `sentinelKind` is a closed switch whose default is `errs.KindInternal` (`port/kind.go:110-129`), and the kind table is closed at nine — `rank`'s default is 0 and `StatusFor`'s is 500 (`port/kind.go:96-99`, `port/porthttp/errors.go:69-70`), so a consumer-defined kind is a 500 and there is no 429 | serious | Adopting the error contract for a domain layer that already exists is the first integration job after mounting, and it costs a fault at every return site. Adding a kind after the tag changes a body a mobile client is already parsing |
| 6 | The cause of a 500 is dropped at `port/porthttp/render.go:129-131` and the `Renderer` contract offers no seam to observe it, so the only place it is still in scope is a custom renderer — which by blocker 4 is per-resource. Separately, `context.Canceled` and `context.DeadlineExceeded` are classified nowhere in the repository and land on `sentinelKind`'s default | serious | Every client disconnect behind a load balancer is a 500 with an empty body and no log line, which is how the error-rate alert stops meaning anything |
| 7 | `ParseEnvelope` has no test anywhere: `grep -rn "ParseEnvelope" --include="*_test.go" .` is empty and there is no `port/porthttp/decode_test.go`. The property it holds — a router's or a gateway's non-envelope body must not become a permanent `crud.ErrNotFound` — rests on a doc comment and on one test a module away (`remote/roundtrip_test.go:333`). `remote` is the only in-repository caller; the surface is exported, so a consumer's gateway is the second | serious | The one property whose comment argues hardest is the one nothing in its own package holds |
| 8 | `WithErrorHandler` is a third silent way to lose the path map: `build` constructs a renderer only when `errorHandler == nil` (`crud/http/crudnet/handler.go:125`), and logging a 500 is exactly what a consumer reaches for it to do | sharp edge | Blocker 2 with a different option name, on the path H-PORT-20 sends people down. No case, no doc row, no test |
| 9 | The retained body reaches the renderer through a context on `net/http` and through an *unexported* Locals key on Fiber (`crud/http/crudfiber/handler.go:460`), and `crudnet.Errors` renders with the request it passed down, not the one the handler holds (`crud/http/crudnet/middleware.go:76-78`). A hand-written handler can supply it on Gin only | sharp edge | Three bindings, three answers. [[FL-013]]:112's `raw-body fallback \| yes \| yes \| yes` is accurate for each binding's *own* routes, so what is needed is a new row for what a hand-written handler can supply, not a correction |
| 10 | `port.Hops` finds a mapper's `errs.Resolver` with a bare type assertion (`port/path.go:60`). A mapper reached through any wrapper that does not embed it loses the path map with nothing to see | sharp edge | [[D-061]]'s invariant is written generally — *"no optional interface is looked up with a bare type assertion on the layer directly below"* — and this is the same failure it was written about, one subsystem over |
| 11 | `DefaultService` never calls its own interface methods, so no override is reachable from another verb: `Replace` calls `s.repo.Save` (`port/service.go:209`) and guards with `s.repo.GetByID` (`:193`), `DeleteMany` calls `s.repo.Delete` (`:229`). `Sanitize` runs *inside* `Create` (`:155`), so an override sees the client-chosen key and any key it sets is erased | sharp edge | A quota or an audit rule at the service seam on a uuid-keyed resource is walked past by `PUT`; a soft delete in `Get` is walked past by `PUT` too. The comment that says so is on `Replace`, which the consumer had no reason to read |
| 12 | `UpdateCommand` carries no `Options` (`port/command.go:50-54`) and `DefaultService.Update` passes none (`port/service.go:178`), while `ListCommand`, `CountCommand` and `GetCommand` all carry them and `Repository.Update` accepts them (`port/repository.go:22`) | sharp edge | The port-side half of the reads-only asymmetry the bindings document. It is the difference between "my tenant scope narrows the update" and "it does not", and nothing states which |
| 13 | `docs/modules/en/port.md:117` and `:161` teach `authorId` — in both the mapper example and the `PathMap` example — where the generator emits `authorID` (`_examples/example/blog/vv_gen.go:115`). The Russian mirror repeats both (`:119`, `:164`). `NewPathMap` validates the map's keys and never its values (`port/pathmap.go:87-113`), so a map copied from the page passes the start-up check and then names a key no client sent | sharp edge | It is a correction of an active error in the page a consumer copies from, and the start-up check that exists to catch exactly this cannot see it |
| 14 | There is no create-many command and no bulk write on `port.Repository` (`port/repository.go:16-25`), while `crud.Repo.SaveAll` exists (`crud/repo.go:43`) | sharp edge | Adopting the service seam for its rules costs the one-statement write the library advertises, and the only escape walks past every rule. A trade the owner should make before a tag |

## Contested

- **"H-PORT-04's must-hold 1 belongs to the codegen sweep."** Half kept.
  `MustPathMap`'s exact-and-total check and the hop's position in the chain
  stayed (now H-PORT-08), because they are `port/pathmap.go`'s; the generator's
  coverage is cross-referenced to H-CODEGEN-08 and H-CODEGEN-10.
- **"Score the one root cause once instead of five times in the DX table."**
  Kept as separate rows, because a reader scanning the table is looking for the
  thing they are about to do, not for the fix. The Overall paragraph says in one
  sentence that the error-side rows are one change — and now also says that the
  rule-side rows are not.
- **"Blocker 3 and half of blocker 4 are the same defect scored twice."** Kept
  apart, and the reason changed: the observer is now a `RenderOption` on
  `NewRenderer` rather than an option on the middleware, so it closes the 500 gap
  without the install-seam fix landing first. Two rows that no longer close
  together are two rows.
- **"The observer dies when a consumer replaces the renderer, so it should be
  the middleware's."** Kept on the renderer. A consumer who wrote the renderer is
  holding the error; the seam exists for the consumer who did not. Putting it on
  the middleware would have made it depend on change 1 and would still have
  missed `build`'s renderer and any per-resource one.
- **"`porthttp.WithResolvers` becomes a lying no-op under the hops-on-context
  change."** Accepted as a defect in the round-1 proposal and fixed rather than
  argued: `EnvelopeRenderer` keeps its `resolvers` field and `port.Violations`
  *prepends* the context's hops, so nothing leaves the exported surface. The
  residual cost — a consumer who wrote `WithResolvers(port.Hops(...))` by hand now
  applies them twice — is stated in "What it must not break" rather than hidden.
- **"H-PORT-06 must-hold 4 (`authorID` vs `authorId`) is codegen's, drop it."**
  Kept, narrowed to the half that is genuinely `port`'s and is in neither sweep:
  `encoding/json` accepts the mis-cased key, so the *declared* hop answers a name
  the client never sent while the raw-body fallback would have answered the right
  one. The stronger mechanism gives the worse answer, and that is a property of
  the path chain, not of the generator. Which convention to emit stays
  H-CODEGEN-10's.
