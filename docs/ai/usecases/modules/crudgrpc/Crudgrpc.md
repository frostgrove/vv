# crud/rpc/crudgrpc — the CRUD API a service mesh can call, without a `.proto` and without `protoc`

**Covers:** `github.com/frostgrove/vv/crud/rpc/crudgrpc`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — a `Replace` whose `entity` key is misspelled overwrites the row with a zero model and answers success, the one snippet the module page gives for the error contract is a silent no-op on all eight methods, a panic in a consumer's own hook takes the whole process down where the three HTTP bindings answer 500, and no tool and no other team's build can see what the service offers. On the client seam, a numeric key past 2⁵³ can select a different row; path-carrier losslessness awaits an Errs-owned codec decision.

## What a consumer is actually trying to do

They already have a resource. It is a struct, a table, a repository, and on the
public edge it is served over HTTP. Now something inside the building needs the
same rows — a job runner, another team's service, a sidecar — and inside the
building everything speaks gRPC. They do not want a second implementation of the
same eight operations, and they do not want the rules to drift between the two
doors. They want the value they already have to answer on a second port.

They do not have one resource. They have fifteen, in eight packages, and the
service they are standing up is the whole API. So the questions that matter are
about the twentieth mount, not the first: what is installed once per server and
what is installed once per resource, what happens when two teams both name a
resource `Article`, and what a rename costs.

The thing they are most afraid of is the toolchain. A gRPC API usually means a
`.proto`, a generator, a build step that needs a C++ binary, and a review every
time a column is added. They arrived here because someone told them this one
needs none of that. What they have not yet worked out is that the review was
doing something: it is where the calling team found out the shape changed.

Some of them arrived for a second reason, and it is worth naming because the
answer is no. They want the second door because gRPC is supposed to be faster
and to stream. Neither is on offer here, and finding that out in a load test
after a service is built on it is the expensive way.

Then there is the ordinary Tuesday work. List with a filter and a sort, page
through it. Fetch one row with its author preloaded. Create something and let
the database own the key. Change one field without wiping the three they did not
send. Load a batch in, delete a batch. When a write collides with a unique
index, the caller wants to know which field, in words it can put next to an
input box, and not a sentence with a table name in it.

Multi-tenancy is not a feature they turn on later. The caller carries a token,
the token says which tenant, and every read has to be narrowed by it before the
first row is read. They expect the same rule to hold on the write side and they
expect to be told plainly if it does not. The presenter that hides `cost_price`
from the reporting team and the hook that stamps a tenant on every write are the
first two things they ask of a mounted resource, and they expect to write each
of them once for both doors.

Finally, they have to run it, and running it means running it inside a mesh. The
process stays up. Kubernetes can tell whether the instance is ready. A failure
the client cannot see is visible in a log they can tie to the caller's trace. A
client that hangs up does not look like a server bug and does not leave a query
running. A retry the mesh performs on their behalf does not create a second row.
A colleague with a Python service or a terminal can call the thing without a
two-day detour. And before any of that ships, they can test it.

## Happy cases

### H-CRUDGRPC-01 — The service I already serve over HTTP, on a second port
**Who:** a backend engineer whose product API is already mounted on Gin
**Wants:** the same resource callable by an internal service, with the same rules, without a second implementation
**Story:** They have a service value with their business checks on it, mounted on Gin behind `/articles`. They add a gRPC listener, register the same value, and expect a `List` over gRPC to make the same decisions the `POST /articles/query` made. They pick their own service name because their mesh has a naming convention. Later they mount the same value a second time, reads only, for the reporting team.
**Must hold:**
1. The value passed to `Register` is the same Go variable passed to `Mount` — one type, no adapter, no wrapper to keep in step.
2. A rule written once refuses the same request on both doors, with the same code and the same field path.
3. A resource name with a package in it — `acme.catalog.v1.Article` — is used verbatim, so the mesh's routing and per-method rules keep working.
4. No `.proto` file and no `protoc` **in this service's build**. What that costs a caller who is not in this process is H-CRUDGRPC-11.
5. A second mount of the same value, reads only, starts — and the name it needs is either derived or the refusal names it.
6. A scope, a presenter and the two write hooks written for the HTTP door are reusable on this one.
**Today:** 🟡 partial
**Evidence:** Rules 1–4 hold. `crud/rpc/crudgrpc/service.go:39` (`Register`), `service.go:25` (`ServiceName`, a dotted name verbatim), `handler.go:21` (`Service` is a type alias for the `port` one, so it is literally the same type). `test/portmount/grpcmount_test.go:112` compares the recorded command across all four transports, and `:346` compares the refusal class; `test/integration/rpc_grpc_test.go:157` runs the service's own rule against a live database and `:168` is its control.

Rule 5 is where the story kills the process. `ReadOnly` itself is right — `options.go:103`, `service.go:70` (the writes are never appended to the descriptor), `handler_test.go:111` whose control leg is that every write then answers `Unimplemented`, and `client_test.go:323` (`TestAnUnregisteredMethodIsNotAMissingRow`). But a second `Register` under the same resource name is a duplicate service registration, and grpc-go answers it with `logger.Fatalf` — `google.golang.org/grpc@v1.83.1/server.go:788`, which is `os.Exit`, not a panic a consumer can recover and not a message naming anything in this library. The reads-only mount therefore needs a *distinct* name, which is then a second name the reporting team's client, the mesh's routing and every per-method rule have to know. `grep -rn "\.Register(" --include='*_test.go' crud/ test/` finds two crudgrpc calls, `test/integration/rpc_grpc_test.go:45` and `:68`, under two names on two servers. Nothing anywhere mounts one value twice.

Rule 6 fails, and it is the rule the reads-only mount would actually be built from. The four bindings' transport options have four different function types: `crud/http/crudnet/options.go:97` takes `*http.Request`, `crudgin/options.go:97` takes `*gin.Context`, `crudfiber/options.go:95` takes `fiber.Ctx`, `crud/rpc/crudgrpc/options.go:86` takes `context.Context` — and the same four-way split for `WithTransform`, `BeforeSave` and `BeforeUpdate`.
**If not ready:** For rule 5, the module page needs one line saying the second mount takes its own name and what grpc-go does when it does not. For rule 6, the cheap half is a documented portable shape — write the rule as `func(context.Context, …)` once and adapt it at the HTTP call site with a one-line closure — in the options section of `docs/modules/en/crudgrpc.md` and `docs/modules/ru/crudgrpc.md`, where the difference is currently presented as a detail ("with `context.Context` where the HTTP ones take their framework's context"). Today a team on the twentieth resource keeps two copies of every presenter and every hook, and `make check-triplets` cannot compare them.

### H-CRUDGRPC-02 — Bound what a client may name, on the constructor I actually used
**Who:** the engineer who owns the resource
**Wants:** filter, sort and paging over one request document, restricted to the fields they chose
**Story:** They send `{"filter":{"status":{"eq":"draft"}},"sort":["-createdAt"],"limit":20}` and get a page back with a total and a `hasNext`. Then they declare which fields may be filtered and sorted, and check that a request naming anything else is refused rather than served. Because they had already assembled their own service value, they mount it with `Serving` and put `WithQuery(cfg)` beside it. The process does not start.
**Must hold:**
1. One document carries the whole DSL — no second door, no query string.
2. The answer carries the page envelope: `items`, `page`, `limit`, `total`, `totalPages`, `hasNext`, `hasPrev` — plus `nextCursor` and `prevCursor` on a cursor walk. Under `skipTotal`, `total` is the page length and `totalPages` is `0` on purpose, and `hasNext` is still honest.
3. A field the model does not have is refused as a client mistake, and the refusal names the field the client wrote.
4. A field outside the declared bounds is refused the same way.
5. The option that bounds it is accepted by the constructor the consumer actually used, or the refusal names the option, the constructor and the fix.
**Today:** 🟡 partial
**Evidence:** `crud/rpc/crudgrpc/handler.go:90` (`List`), `crud/page.go:5-19` (the envelope, cursors included), `message.go:77` (the whole request read as the DSL). `crud/sqlrepo/repository.go:173-196` fetches one row past the page under `skipTotal` for exactly the reason rule 2 states, and sets `TotalPages` to `0`.

Rule 3's *class* is pinned across transports at `test/portmount/grpcmount_test.go:346`; `:373-386` asserts the code and that the message does not contain `widgets`, and never looks at the path. What is genuinely unpinned is narrow: no test on this transport asserts that a **query refusal** (`*query.Error`) names the path the client wrote. The path hop itself is pinned — `handler_test.go:410` (`TestAServicePathHopReachesTheRenderedField`) asserts the rendered field is `label` with a control at `:436` asserting it is `Name` without the mapper, so a renderer that dropped the path fails both legs. `status_test.go:216` is the one that would pass with every `Field` empty: it compares order and cardinality only.

Rule 4 has no test on this transport. `client_test.go:31` uses `WithQuery` as a fixture to switch `AllowUnpaged` on, and the bounding itself is proved in `port/service_test.go:384`. The three HTTP bindings each carry `TestWithQueryBoundsWhatClientsMayAskFor`; this one does not.

Rule 5 is right and pinned: `handler.go:65` and `:72` call `o.RefuseServiceOptions("crudgrpc.Serving")`, `port/rules.go:91` panics naming both, and `handler_test.go:445` asserts the message contains the option name and the constructor name, with a control at `:470` that the same option on `New` is accepted. That is [[D-021]] working. What fails is discovery: `docs/modules/en/crudgrpc.md:83` and `:89` list `WithQuery` and `AllowClientID` in the option table with no note, and the only page that mentions the panic is `docs/modules/en/port.md:249`, which a consumer mounting a gRPC resource has no reason to open. [[FL-013]]:317 has the row, in the flow rather than the module page.

Rule 3 also collides with a claim elsewhere: [[UC-015]] guarantee 5 is "a 400 caused by the query document names the offending path", its actor is explicitly a client reading a status over HTTP **or gRPC**, and `docs/ai/usecases/Index.md:86` lists it as `covered`. On this transport it is not pinned. One of the two is wrong.
**If not ready:** Two footnotes in both option tables — these two configure the service, so with `Serving` they go to `port.NewService` — plus two tests named like the HTTP ones. Two open gaps under [[UC-002]] reach this transport unchanged, because gRPC compiles the same document through the same compiler: the preload allow-list is not checked hop by hop (`docs/ai/usecases/Index.md` gap 10), and `docs/ai/usecases/Index.md` gap 20's first half — search predicates and select entries are not charged against the condition budget, the search-field list has no length cap, and both counters restart inside a preload's own filter. [[UC-002]]'s own status is *partially covered* and this case inherits that. Gap 20's other half is H-CRUDGRPC-13's; it is one index entry, not two.

### H-CRUDGRPC-03 — One row, with its relations, and the row that is not there
**Who:** a service rendering a detail view for another service
**Wants:** `Get` by key with a preload, and an unambiguous answer when the key names nothing
**Story:** They ask for article 42 with its author, then ask for one that was deleted an hour ago, and want the second answer to be something they can branch on without parsing a sentence.
**Must hold:**
1. The key travels as a string and survives being large.
2. `preload` and `select` on a keyed read are honoured.
3. A missing row is `NotFound`, distinct from every other failure, with no table name in the message.
4. A key that does not parse answers `InvalidArgument` and no statement is issued.
**Today:** ✅ ready
**Evidence:** `handler.go:130` (`Get`), `message.go:100` (`idOf`: a string or a number, then `port.CoerceID`), `message.go:86` (the nested `query`). `handler_test.go:228` pins the large-key round trip with a control that the same number *inside* an entity does lose precision; `handler_test.go:259` pins the unparseable key against a fake that records every call; `status_test.go:165` pins that no status message names the entity. The preload is walked at `test/integration/rpc_grpc_test.go:130` against a live database — `test/portmount/grpcmount_test.go:129` sends `select` only, and nothing in this module's own tests names a preload at all.
**If not ready:** —

### H-CRUDGRPC-04 — Create a row and let the database own the key
**Who:** an engineer adding a write path for an internal producer
**Wants:** a create that cannot be tricked into choosing its own id or back-dating a timestamp
**Story:** They call `Create` with the entity document. A misbehaving client sends `id` and `createdAt` too. They want both dropped, and they want the stored row back — including whatever the database filled in. One resource genuinely wants client-chosen keys, so they turn that back on for it.
**Must hold:**
1. A client-sent primary key is cleared where the database generates it.
2. A `generated` column sent by the client is cleared.
3. The answer is the row as stored, not the request echoed.
4. `crudgrpc.AllowClientID()` on a handler makes the client's key the stored key.
**Today:** 🟡 partial
**Evidence:** Rules 1–3: `handler.go:157` (`Create`; the clearing is `port`'s and below the binding), `handler_test.go:333` (`TestACreateIsClearedBelowTheBinding`), `handler_test.go:305` (`TestReplaceIsNotAWayAroundAllowClientID`). Rule 4 is a declaration only: `options.go:109`. Nothing in this module creates a row through the gRPC handler with `crudgrpc.AllowClientID()` and asserts the key survived. `handler_test.go:323` uses `port.AllowClientID()` on the service directly — which proves `port`'s behaviour, not this binding's wiring of it — and `handler_test.go:452` and `:470` name the option only inside the panic table and its no-panic control. The wiring it would exercise is `port/rules.go:70` (`Rules.Service()`).
**If not ready:** The option almost certainly works; the point is that nothing here would notice if `Rules.Service()` stopped translating it. `crud/http/crudnet` carries `TestAllowClientIDLetsTheClientChooseTheKey`; this binding needs the same name over a `Create` call.

### H-CRUDGRPC-05 — Change one field without wiping the others
**Who:** anyone who has ever had a form blank a column
**Wants:** three states on a nullable column: leave it, clear it, set it
**Story:** They send `{"id":"42","patch":{"title":"new"}}` and expect the summary column untouched. Then they send `{"patch":{"summary":null}}` and expect the column cleared. The two must not be the same thing.
**Must hold:**
1. A key the patch does not carry is not written.
2. A key carrying an explicit null writes NULL.
3. The two are distinguishable at the wire, not only in Go.
4. A patch that changes nothing issues no UPDATE and answers the row as it stands.
**Today:** ✅ ready
**Evidence:** `message.go:42` (`fromStruct` goes through `protojson` then `encoding/json`, so the model's own tags decide and `crud.Opt` keeps its three states), `handler.go:175` (`Update`), `crud/sqlrepo/repository.go:729` (no changes, current row, no statement). `handler_test.go:168` pins all three states with the control that an absent key and a null produce two *different* ones — [[UC-003]] guarantee 12 on this wire shape. `client_test.go:156` walks the null through a real client.
**If not ready:** — but read H-CRUDGRPC-14 before treating rule 4 as good news. Rule 4 is a deliberate, shipped guarantee; what H-14 asks is how a client tells it apart from a key the server did not recognise.

### H-CRUDGRPC-06 — Replace, delete, and clean up a batch
**Who:** a retention job
**Wants:** delete one, delete a list, and a cap so a bad loop cannot ask for a million
**Story:** Nightly, the job collects expired ids and calls `BulkDelete`. Sometimes the list is empty. Sometimes a colleague pastes 5000 ids into a script.
**Must hold:**
1. Deleting one row that is not there is `NotFound`; deleting a set that matched nothing is `{"deleted": 0}`, not an error.
2. An empty id list answers `{"deleted": 0}` and issues no DELETE — and a request that *omits* the list is not the same request.
3. A list past the cap is refused as a client mistake, and there is a cap even when nobody set one.
4. `Replace` takes the key from the request and never creates a row where the database owns the key.
**Today:** 🟡 partial
**Evidence:** Rules 1, 3 and 4: `handler.go:228` and `handler.go:244`; `handler_test.go:284` (`TestDeletingNothingIsAMissForOneRowAndZeroForASet`), `handler_test.go:499` (`TestMaxBulkCapsOneRequest`), `options.go:114` and `port/rules.go:61` (`BulkCap`, 1024 when nobody said — [[D-060]] chose the non-zero default because zero meant unlimited), `port/service.go:231` (an empty set short-circuits above the repository), `handler_test.go:305` (Replace).

Rule 2's second clause fails. `idsOf` returns `(nil, nil)` when the key is absent (`message.go:114-118`), so `{"idz": [...]}` answers `{"deleted": 0}` with no error and no statement — indistinguishable from the correct empty-set answer. `idOf` returns `missing id` for the same situation (`message.go:102-105`), so two keys of the same API disagree about what an absent key means. A nightly job that silently deletes nothing reports success to its scheduler forever.
**If not ready:** `idsOf` answering the way `idOf` does is a one-line change with a behaviour cost: a client sending `{}` today gets a success and would get a refusal. That is the same decision H-CRUDGRPC-14 asks for and belongs with it.

### H-CRUDGRPC-07 — A collision the caller can point at a field, in the caller's language
**Who:** a service that proxies a form submission
**Wants:** a conflict that names the field, with something it can switch on
**Story:** Two callers create the same SKU. The second gets a failure. The caller needs to mark the `sku` input, in the user's language, and it must not receive the driver's sentence or the table name. They install the message catalogue the way the module page shows. Separately, a caller holding a versioned row loses a race and needs to know to re-read rather than to mark a field.
**Must hold:**
1. The status code says conflict, not "unknown" and not "internal".
2. The details carry one entry per violation, with the field path and a stable machine code spelled the same as the HTTP envelope's.
3. Nothing internal is in the message: no table, no column, no driver text.
4. An internal failure carries no details at all.
5. A catalogue installed the way the module page shows translates the sentence the eight methods answer with.
6. A lost update is distinguishable from a duplicate key.
**Today:** 🟡 partial
**Evidence:** Rules 1–4: `status.go:237` (`Render`; the internal short-circuit at `:242` happens before anything is copied out of the fault), `status.go:294` (the `BadRequest` / `ErrorInfo` details), `status.go:74` (the kind→code table). `status_test.go:165`, `status_test.go:141`, `status_test.go:416` (the captured corpus on four engines), `test/portmount/grpcmount_test.go:283` (the code is the identical string on both transports).

Rule 5 fails, and it fails silently on the one snippet a consumer copies. `docs/modules/en/crudgrpc.md:111-118` and `docs/modules/ru/crudgrpc.md`'s mirror open the errors section with `Errors(crudgrpc.WithMessages(catalogue), crudgrpc.WithCodes(codes))`. Every failure a CRUD method produces is rendered by the handler's own renderer first — `handler.go:312-313` (`fail` → `h.render.Render(...).Err()`) — and `interceptor.go:33-35` passes through any error that already carries a status. So the catalogue on `Errors` reaches the consumer's own hand-written methods and none of the eight. The handler's renderer is `defaultRenderer = NewRenderer()` with no options (`options.go:124`, `:129-133`), so `WithCodes` on `Errors` never sharpens a CRUD kind either. The module's own locale test bypasses the documented seam and wires the catalogue through `WithRenderer[Widget, int64, WidgetUpdate](NewRenderer(WithMessages(cat)))` on the handler (`handler_test.go:581-582`), which is why it passes. Locale *detection* is free and correct — `handler_test.go:565` and `:608` prove the metadata reaches the ladder — but a translated sentence is not.

Rule 6 holds only through the details: `port/kind.go:163` maps `crud.ErrStaleVersion` to `errs.CodeStaleVersion` with kind `Conflict`, and `status.go:84` sends every conflict to `AlreadyExists`, so a stale version, a unique collision and a restrict violation arrive under one status code and are told apart by `ErrorInfo.Reason` alone. [[D-052]] accepted that collapse deliberately and says so.
**If not ready:** For rule 5 the consumer's real cost today is: build a `Renderer`, pass `WithRenderer[M,ID,U]` to all fifteen handlers, and pass the same options *again* to `Errors` for their own methods. The usage guides teach the working seam for HTTP (`docs/usage-guides/gorm.md:838`, `ent.md:900`), so a consumer moving from a guide to the gRPC page is steered onto the seam that does not apply. Rule 6's collapse is decided, not broken; what is missing is a paragraph telling a consumer to switch on the reason rather than the code for this one case, next to the `InvalidArgument` collapse already explained on both module pages.

### H-CRUDGRPC-08 — The caller's identity, and their tenant
**Who:** a SaaS backend
**Wants:** a token on the call, a principal in the context, and per-resource rules that can tell two resources apart
**Story:** They add the auth interceptor, wire a policy that maps roles to permissions, and mount CRUD behind it. They also have one streaming method of their own on the same server. They expect a bad credential to be `Unauthenticated` everywhere.
**Must hold:**
1. The per-method rules can tell `Article/Create` from `Comment/Create`.
2. Wiring it is the snippet in the docs, and the snippet is right — including the order the interceptors are chained in.
3. `WithScope` is pinned here the way it is pinned on the other three bindings, control included: under a scope of `TenantID = 7`, `Get` on another tenant's row answers `NotFound` while `Delete` on that same row answers `{"deleted":1}`.
**Today:** 🟡 partial
**Evidence:** Rule 1 holds by construction: per-resource service names give the full method name a rule keys on (`service.go:20`, and [[D-052]] chose the shape for exactly this).

Rule 2 is where it slips. The unary half of the documented snippet is correct and the order is load-bearing in a way nothing says: `crudgrpc.Errors` renders only what it wraps (`interceptor.go:28`), and grpc-go runs `interceptors[0]` outermost (`google.golang.org/grpc@v1.83.1/server.go:1245-1249`), so `Errors()` must be listed before `authgrpc.Unary(guard)`. `docs/modules/en/authgrpc.md:56` happens to get it right and no test pins it — reverse the two and a refused credential answers `Unknown` on unary calls. The stream half is wrong as documented: `authgrpc.md:57` mounts `ChainStreamInterceptor(authgrpc.Stream(guard))` alone, with no `crudgrpc.StreamErrors` (`interceptor.go:53`), which is the wiring that answers `Unknown` instead of `Unauthenticated`. The mechanism worth stating is that `Errors` is *optional* for the eight methods: each renders its own status inside `fail`, so a consumer who omits it — or lists it after `authgrpc.Unary` — sees all eight answering correctly, and only auth refusals and their own methods degrade.

Rule 3 has no test on this transport at all. `grep -rn WithScope crud/rpc/crudgrpc` finds `options.go:86` and nothing else, where the three HTTP bindings each carry `TestWithScopeNarrowsEveryRead`, `TestWithScopeIsANDedWithTheClientFilter`, `TestWithScopeReachesTheReadsAndSaysNothingAboutTheWrites`, `TestAScopeThatFailsIsMappedLikeAnyOtherError` and the asymmetry control `TestARowHiddenFromReadsIsStillDeletableByID`. The asymmetry itself is stated in both module pages' `WithScope` row and in `options.go:76-85`, so what is missing is the proof, not the disclosure.
**If not ready:** Row-level isolation itself is [[UC-004]]'s and `security.Gate`'s, not this binding's, and it is open there. What crudgrpc owes is narrow and cheap: an order note beside the chain in `docs/modules/en/crudgrpc.md` and `docs/modules/ru/crudgrpc.md`, `StreamErrors` named in this module's errors section, and the five `WithScope` tests. The `authgrpc.md` half of the stream snippet is the same finding the authhttp sweep records as *"the documented gRPC stream wiring omits `crudgrpc.StreamErrors`, so a refused stream answers `Unknown`"* — one bug, two sweeps, fix it once.

### H-CRUDGRPC-09 — Hide a column, and stamp the caller's tenant on every write
**Who:** the engineer who mounted the reporting team's copy of the resource
**Wants:** a presenter that removes `cost_price` from everything that leaves, and a hook that fills `tenant_id` before anything is stored
**Story:** They pass `WithTransform` so the reporting mount never returns `cost_price`, on a list page, on a keyed read, and on the row a `Create` echoes back. They pass `BeforeSave` and `BeforeUpdate` to stamp the tenant from the principal. Later they change the request document to a DTO of their own and mount with `NewFor`.
**Must hold:**
1. The presenter runs on every read shape — page, keyed read, count is not an entity.
2. The presenter runs on the row a write echoes back, so a `Create` response cannot leak the column the presenter exists to hide.
3. `BeforeSave` runs after the server-owned fields are cleared, and its mutation reaches the repository.
4. `BeforeUpdate` sees the key from the request and its mutation lands.
5. A refusal from either hook is classified like any other failure, not as `Internal`.
6. A DTO of the consumer's own reaches the model through the mapper, and a violation on it names the key the client sent.
**Today:** 🟡 partial
**Evidence:** Rule 1 is pinned: `handler_test.go:474` (`TestWithTransformHidesColumnsOnEveryReadShape`). Rule 6 is pinned twice: `handler_test.go:388` (`TestADistinctInputDTOReachesTheModelThroughTheMapper`) and `handler_test.go:410` with its control at `:436`.

Rules 2–5 have no test here. The wiring exists and reads correctly — `handler.go:290-295` (`entity` renders through the presenter, and every write path returns through it), `handler.go:271-288` (`beforeSave` and `beforeUpdate` bind the call's ctx and key), `port/service.go:204-208` (the hook runs after `ClearGenerated` and `SetID`) — but the three HTTP bindings carry `TestWithTransformAppliesToWritesToo`, `TestBeforeSaveMutationReachesTheRepository`, `TestBeforeSaveCanRefuseTheRequest`, `TestBeforeUpdateSeesThePathIDAndItsMutationLands` and `TestTheHookStillRunsAfterTheServerOwnedFieldsAreCleared`, and this one carries none of them. If rule 2 stopped holding, a `Create` or `Update` response would echo the stored row with exactly the columns the presenter exists to hide, and nothing in this module would fail.
**If not ready:** Five test names, copied from `crud/http/crudnet` and spelled in this binding's vocabulary. Rule 2 is the one worth writing first: it is a disclosure rather than a wiring miss, and it is the same class as the scope control H-CRUDGRPC-08 asks for.

### H-CRUDGRPC-10 — Another Go service holds it as a repository
**Who:** the team that consumes the resource
**Wants:** to call it with the code they would have written against a local repository
**Story:** They hold the far resource as a value, call `Get` with `crud.Where(...)`, and branch on `errors.Is(err, crud.ErrNotFound)` exactly as they would locally. Then they need to forward each incoming caller's own token on the outbound call, on a server that fans out for many callers at once. Then the far side redeploys.
**Must hold:**
1. No generated stub on this side either.
2. A filter written in Go arrives as the same narrowing the far side would have received locally.
3. `crud.ErrNotFound` and a conflict come back as themselves, with their violations.
4. A status this library did not write — `Unimplemented`, an interceptor's refusal — is never mistaken for a classified failure, including when the resource was renamed on the far side.
5. Two in-flight calls through the same held resource carry two different tokens, and neither borrows the other's.
6. The far side redeploying is distinguishable from the far side refusing, and the retry hint it sends is readable.
**Today:** 🟡 partial
**Evidence:** Rules 1–3: `transport.go:34` (`Transport`), `transport.go:98` (the request documents, field for field with the handler), `transport.go:203` (`fault`: the `ErrorInfo` domain is what tells this library's status from anyone else's). `client_test.go:49` walks all eight methods; `:202` pins the filter; `:224` the conflict with its violations; `:292` the `InvalidArgument` collapse undone by the code; `:323` the `Unimplemented` case.

Rule 4 holds with one deliberate exception the documents never state: a detail-less `codes.Internal` — which is what a foreign interceptor, a proxy or grpc-go itself can answer — becomes a classified `errs.KindInternal` fault rather than a `*remote.ProtocolError` (`transport.go:235-240`, and the comment says why: "the silence is the message"). So "any status without this library's `ErrorInfo`" is false for exactly one code. `client_test.go:266` exercises only this library's own `Internal`. Rule 4's rename half has nothing behind it at all: `Register(srv, "Article")` and `Transport(conn, "Article")` agree only because two string literals in two repositories match. There is no shared constant and no start-up check, so a rename is a green build on both sides and `Unimplemented` at 3am.

Rule 5 does hold, through the option the module page names — but through a shape no document shows. `grpc.PerRPCCredentials` is a `CallOption`, not only a `DialOption` (`google.golang.org/grpc@v1.83.1/rpc_util.go:484`, `:499-501`; `stream.go:365-366`), and its `GetRequestMetadata` runs with each call's own context at stream creation (`internal/transport/http2_client.go:712`). So `crudgrpc.WithCallOptions(grpc.PerRPCCredentials(forwarder{}))` — set once at construction, appended into `t.call` and passed on every `Invoke` (`transport.go:56`, `:85`) — is genuinely per-call and reads the ctx `remote.Resource` already passes through. What is missing is the example, and two caveats: `PerRPCCredsCallOption` is marked EXPERIMENTAL in grpc-go, and `RequireTransportSecurity()` must be false on an insecure connection. `metadata.NewOutgoingContext` before the call is the other channel and is also undocumented. `grep -n "WithCallOptions\|metadata" crud/rpc/crudgrpc/client_test.go` is empty.

Rule 6 fails. A rolling restart arrives as a `*remote.ProtocolError` carrying `Status: "Unavailable"` — the same *type* as "the far side is read-only", told apart only by that string (`remote/transport.go:85-86`: "Status is the transport's own word for what came back"). The `RetryInfo` the far side attaches to every `Unavailable` (`status.go:334`) is read by Envoy and dropped on the floor here: `transport.go:222-232` reads `ErrorInfo` and `BadRequest` and nothing else. A consuming team's first production incident is the resource they depend on deploying, and today they cannot branch on it except by string-matching `ProtocolError.Status`.
**If not ready:** For rule 5, two lines and a snippet on both module pages, replacing the bare phrase "per-call credentials" in the option table. For rule 6, either a `remote` classification for `Unavailable` — which is `remote`'s and reaches the HTTP client too — or, at minimum, a sentence saying `ProtocolError.Status` is the field to branch on and what the words are. Reading the `RetryInfo` is additive and would give the caller the delay the mesh already honours.

### H-CRUDGRPC-11 — A Python service, and grpcurl on Tuesday morning
**Who:** a colleague on a team that is not a Go team
**Wants:** to call the resource from their language, or poke at it from a terminal
**Story:** They are given a host, a port and a service name. They point grpcurl at it, as the example's own instructions tell them to. Then they go looking for something to generate a client from.
**Must hold:**
1. `grpcurl -d '{"limit":2}' host:9090 vv.crud.v1.Product/List` works, or no document tells them to try it.
2. There is one place that says what a non-Go caller does instead, and it is where they will read it first.
3. Whatever the answer is, it does not require them to hand-maintain a file this repository could have generated.
**Today:** ❌ missing
**Evidence:** There is no descriptor and no `.proto` anywhere: `find . -name '*.proto' -o -name '*.pb.go'` is empty and `grep -rn "grpc/reflection" .` returns nothing. `_examples/pgx-grpc/main.go:21-23` instructs the reader to call two methods with grpcurl; grpcurl resolves a method through reflection, `-proto` or `-protoset`, and this server offers none of the three, so both commands fail before a byte is sent. Six places carry the weaker claim that reflection "cannot list the methods" — `docs/modules/en/crudgrpc.md:196`, `docs/modules/ru/crudgrpc.md`'s mirror, `docs/usage-guides/gorm.md:782`, `docs/usage-guides/ent.md:844`, `docs/ai/flows/FL-013…:113` and `crud/rpc/crudgrpc/doc.go:46` — and none of them says it cannot call them either.
**If not ready:** The callability half is smaller than it looks and should be stated correctly, because over-stating it sends a consumer down a road that costs a file per resource. A non-Go client needs no `.proto`: every method is `google.protobuf.Struct` in and out, which is a well-known type shipping in every protobuf runtime, so Python is `channel.unary_unary('/vv.crud.v1.Product/List', request_serializer=Struct.SerializeToString, response_deserializer=Struct.FromString)` and Java, Node and C# have the equivalent — the same stubless invoke this module's own client uses (`transport.go:83`, and the doc comment at `transport.go:30` says so). What is genuinely missing is everything built on a descriptor: grpcurl, evans, a schema registry, a generated stub someone can code-review, and the compatibility CI most gRPC shops run. That is the cost, and it is a governance cost rather than a connectivity one — H-CRUDGRPC-17 prices it. Until reflection exists, fix the seven places above and start with the example that cannot run.

### H-CRUDGRPC-12 — Ship it, and know why a call failed
**Who:** whoever is on call
**Wants:** the process to survive a bug, and a failure the client cannot see to be visible somewhere they can find it
**Story:** A presenter dereferences a nil on one row out of ten thousand. A connection pool is exhausted at 3am and clients see `Internal`. A caller with a one-second mesh deadline hangs up mid-query. They want, in order: the server still running, a log line they can tie to the client's trace, their dashboard not showing a client's cancellation as a server fault, and the abandoned query to stop.
**Must hold:**
1. A panic in a presenter, a hook or a custom renderer does not take the process down.
2. An `Internal` answer, which by design tells the client nothing, tells the server something — and the line carries the full method, the resource and the caller's trace, not the word `internal` alone.
3. A cancelled or timed-out call is not classified as a server bug.
4. When the caller hangs up, the statement stops: the call's ctx, deadline included, is the ctx the query runs under.
**Today:** ❌ missing
**Evidence:** (1) grpc-go recovers nothing — the only `recover()` in `google.golang.org/grpc@v1.83.1` is in an xds channel — and neither `unary` (`service.go:86-101`) nor `Errors` (`interceptor.go:23`) recovers. The three HTTP bindings all do it inside the same-named `Errors` middleware the consumer already installs: `crud/http/crudnet/middleware.go:63-75`, `crudfiber/middleware.go:32`, `crudgin/middleware.go:46`, pinned by `TestAPanicInTheRendererBecomesASilent500` in each. [[FL-013]]'s difference table has no row for it. It is worse here than the parity gap suggests: `Errors` is optional, and the module page's own mount installs only the unary half (`docs/modules/en/crudgrpc.md:26`), so a consumer who followed the documentation loses the process to a panic in a streaming method as well.

(2) `status.go:242-247` returns the silent `Internal` without logging, and the original error is gone by the time it reaches any interceptor, so a consumer's logging interceptor sees `internal` and nothing else. The second half of the rule is a hole nothing in the repository closes: no gRPC interceptor anywhere installs a request-scoped logger, so `port.Logger(ctx)` answers `slog.Default()` (`port/log.go:26-32`) and the line has no full method, no resource, no peer and no trace id. The only `port.Logger` call in this module is `status.go:264`, for a detail that would not marshal. The authhttp sweep found the same hole on HTTP.

(3) `port/kind.go:110` (`sentinelKind`) has no arm for `context.Canceled` or `context.DeadlineExceeded`, so both fall to `KindInternal` and answer `codes.Internal` where gRPC has `Cancelled` and `DeadlineExceeded`.

(4) holds, and there is nothing pinning it. Nothing in this binding replaces the ctx: `unary` passes grpc-go's own call ctx straight through (`service.go:88-99`), every method threads it to `h.svc`, and `port` hands it to the repository. A deadline set by the mesh therefore reaches the driver. What makes it worth a rule is that a future recovery or logging wrapper detaching the ctx would break it and nothing would notice.

Neither (1) nor (2) appears on `docs/roadmaps/Roadmap.md`, whose only gRPC line is about a CI dependency gate.
**If not ready:** (1) and (2) need no new API and no new dependency. Put the `defer recover()` in `unary` (`service.go:86-101`) rather than only in `Errors`: `Register` installs `unary` on every method unconditionally, it covers the decode hop at `:90` that runs before any interceptor, and it removes the "only if you installed the interceptor" qualifier the HTTP bindings cannot avoid and this one can — then add it to `Errors` and `StreamErrors` as well for the consumer's own methods. Log the internal at `status.go:242` before the status is built. For the seam half, one sentence and one interceptor example on both module pages showing `port.WithLogger` on the incoming ctx turns that log line from a word into a diagnosis. (3) is `port`'s and reaches all four transports. (4) needs a control test, not a change. Today the consumer adds a third-party recovery interceptor, which is a dependency this repository otherwise does not have, and writes a `Renderer` of about ten lines that logs before delegating — a renderer that then covers the eight methods and not `Errors`.

### H-CRUDGRPC-13 — How much comes back
**Who:** the same engineer who bounded the fields in H-CRUDGRPC-02
**Wants:** a ceiling on the size of one answer, not only on what a client may name
**Story:** They review the endpoint through its `query.Config`, sign it off, and ship. A client sends `{"limit": 1000000}`. The page that comes back is larger than the caller's connection will accept.
**Must hold:**
1. There is a maximum page size, and it is reviewable in the same place the field bounds are.
2. A page too large for the caller's connection is refused as the class the decision names, not as an unclassified protocol failure.
**Today:** 🟡 partial
**Evidence:** Rule 1 fails as stated, and [[D-060]] is why it is stated wrongly rather than why it fails. `query.Config` (`crud/query/compile.go:31-81`) bounds depth, conditions, preloads, `in` values, sort terms and the five allow-lists and has no page cap, and D-060 decided that on purpose: *"These are the ceilings that stop a statement no engine will accept, not page sizes — an endpoint that wants a page size says so with `sqlrepo.MaxLimit`."* So the knob is where the decision put it. What is still broken is what `docs/ai/usecases/Index.md` gap 20's second half records: `sqlrepo.MaxLimit` is off by default (`crud/sqlrepo/blueprint.go:53`, "Zero disables the cap"), so an endpoint reviewed through its query configuration alone is reviewed through the wrong file and the default arms nothing. Moving the cap into `query.Config` is a challenge to D-060, not a fix; documenting the pairing is not.

Rule 2 is a conformance failure against a binding decision. [[D-063]]'s Invariant covers **both** directions — "No transport reads a request body, **or a response body**, without a byte cap … refused before it is parsed, with `errs.CodeTooLarge` and `errs.KindTooLarge` — 413 over HTTP, `ResourceExhausted` over gRPC" — and its *where it lives* list names `crud/rpc/crudgrpc/status.go:CodeFor` and `:KindForCode`, `ResourceExhausted`, both directions. On the wire that arm is unreachable for the case that produces it: a page past the client's default 4 MiB `MaxCallRecvMsgSize` (`google.golang.org/grpc@v1.83.1/clientconn.go:139`) fails as a grpc-generated `ResourceExhausted` carrying no `ErrorInfo`, and `transport.go:235-246` turns it into a `*remote.ProtocolError` with no kind at all. `remotehttp`'s `MaxResponse` is the client-side half D-063 names on HTTP; there is no counterpart here.
**If not ready:** Either `transport.go`'s `fault` classifies a detail-less `ResourceExhausted` as `KindTooLarge` the way it already classifies a detail-less `Internal`, or D-063 is amended to say the response direction is the request direction's problem on this transport. It cannot stay as it is: a decision doc is binding and this is a gap against one, not a missing row in a flow. Separately, both module pages need a sentence saying `sqlrepo.MaxLimit` is what caps a page and that its default is no cap.

### H-CRUDGRPC-14 — What tells my caller they got a field name wrong
**Who:** a client team integrating against the resource
**Wants:** to find out at the wire that they misspelled a key, rather than in a support ticket
**Story:** They send `{"nmae":"bolt"}` to `Create` and get a success carrying an empty name. Later they send `{"id":"42","patch":{"titel":"new"}}` and get a success carrying the row unchanged. Then somebody types `{"id":"42","entty":{…}}` on a `Replace`.
**Must hold:**
1. A key an entity document does not define is refused, or something on one of the two sides can see it is wrong.
2. The same for a patch document.
3. A key a client did not send and a key the server did not recognise are told apart from a change the server applied.
4. No spelling mistake in the envelope destroys data.
**Today:** ❌ missing
**Evidence:** The query document is strict and its own comment says why: `crud/query/request.go:84` decodes with `DisallowUnknownFields` because "a client that writes 'filtr' instead of 'filter' produces a document with no filter at all … that is the one failure a client cannot see". [[D-013]] is the decision behind it, it already owns the query document's own keys, and it already weighed and rejected the forward-compatibility trade — *"that trade is available and was not taken, because 'tolerate' here means answer a different question than the one asked"*. Its **What it forbids** list says nothing about entity bodies.

The entity and patch documents get no such treatment. `message.go:50` is a plain `json.Unmarshal`, and `crud/query/request.go:89` is the only `DisallowUnknownFields` in the tree outside tests. `crud/sqlrepo/repository.go:729` then returns the current row when the patch produced no changes, so a misspelling and a deliberate no-op are the same answer, and H-CRUDGRPC-05's rule 4 blesses the no-op correctly.

Rule 4 is the destructive member of the family and it is gRPC-only. `sub` returns `(nil, nil)` for an absent key (`message.go:60-67`), `fromStruct` on a nil Struct leaves the target untouched (`message.go:42-45`), and `Replace` then hands a zero `In` to the mapper and a zero model to the service (`handler.go:209-221`). `port/service.go:190-212` checks the row exists, clears the generated columns, sets the key from the request and saves. So `{"id":"42","entty":{…}}` returns a success and blanks every column of row 42. HTTP has no such hole: a `PUT` body *is* the entity, so there is no envelope key to misspell.
**If not ready:** Two ways out for rules 1–3, both decisions rather than patches: make the entity and patch decoders strict the way the query decoder is — which is [[D-013]] extended to the bodies it deliberately does not cover, a behaviour change on all four bindings, and it breaks a client that sends an extra field today — or ship the descriptor of H-CRUDGRPC-11 so a generated stub refuses it at the caller's compile time. Rule 4 is not one of those: an absent `entity` on `Replace` is a client mistake by every reading, and `sub` refusing it the way `idOf` refuses an absent `id` is a small local change that costs nobody a working request. The same argument covers `idsOf` in H-CRUDGRPC-06.

### H-CRUDGRPC-15 — Fifteen resources on one server
**Who:** the platform engineer standing up the whole internal API
**Wants:** to mount the catalogue, not one resource, and to know what is per-server and what is per-resource
**Story:** They call `Register` fifteen times across eight packages behind one `grpc.NewServer`. They want one error contract, one auth chain, and a custom renderer that logs. Two of the fifteen are called `Article` and come from different teams.
**Must hold:**
1. There is one snippet, in the place a consumer reads first, mounting two resources on one server — and nothing in the per-resource call takes the `*grpc.Server`, so what is per-server is visible by shape.
2. One custom renderer covers the whole server — the eight methods of every resource and the hand-written ones.
3. Two teams can both own a resource called `Article` without colliding, and a collision is a failure somebody can act on.
4. Authorization can be written per resource and verb before the server is built.
**Today:** 🟡 partial
**Evidence:** Rule 1's split exists — `Errors` is one interceptor on the server (`interceptor.go:23`), `Register` is one call per resource (`service.go:39`), and installing `Errors` twice renders once (`handler_test.go:516`) — but the artefact does not: `docs/modules/en/crudgrpc.md:24-30` and its Russian mirror show one resource on one server, which is the single-resource framing that hid rule 2.

Rule 2 fails twice over. `WithRenderer` is an `Option` per handler (`options.go:66`) and `Errors` takes `RenderOption`s (`interceptor.go:23`), two different vocabularies, so the value is passed fifteen times and then once more in a type it cannot be. And even a type-compatible `Errors` would not close it: the handler renders first and the interceptor passes an already-rendered status through, which is the same mechanism H-CRUDGRPC-07 rule 5 fails on. `doc.go:71-73` states the split deliberately — "[WithRenderer] is the whole seam, and [Errors] is the same seam for the methods this package did not write" — so this is a design to revisit, not an oversight.

Rule 3's first half holds: a dotted name is used verbatim (`service.go:25`), so `acme.catalog.v1.Article` and `bikes.v2.Article` coexist, and the bare-name default `vv.crud.v1.` (`service.go:20`) is the only thing that collides. The second half does not: a collision is `logger.Fatalf` inside grpc-go (`server.go:788`), an `os.Exit` at start-up carrying grpc-go's message, not a vv panic a test can assert on with `recover()` and not a message naming this library. The same happens to any `Register` after `Serve` (`server.go:786`). [[D-021]]'s whole argument is about what a start-up failure looks like, and this one looks like nothing this repository wrote.

Rule 4 does not hold, and the evidence it used to be credited with was the wrong symbol. `authgrpc.Skip` (`auth/rpc/authgrpc/interceptor.go:19-27`) is an exemption from **authentication** — a named full method is let through unauthenticated, and `Unary` at `:48-58` has no other branch. There is no per-method authorization symbol in that package. Authorization per entity and verb is `security.Gate`'s permissions, or an interceptor the consumer writes over the full method name that `ServiceName` makes predictable.
**If not ready:** For rule 1, one snippet with two resources on both module pages. For rule 3, one sentence saying the second mount needs its own name and what grpc-go does otherwise. For rule 4, one sentence pointing at `security.Gate` — a platform engineer reading "per-method rules" today goes looking for a policy API that does not exist. Rule 2 is the expensive one and is costed below.

### H-CRUDGRPC-16 — The mesh retries the Create
**Who:** whoever wired the Envoy retry policy, six months before this service existed
**Wants:** an automatic retry not to produce a second row
**Story:** The pool is exhausted at 3am, exactly as in H-CRUDGRPC-12. The server answers `Unavailable` with a one-second retry hint. The sidecar retries. The client never knew.
**Must hold:**
1. A write that is retried by something outside the application does not create a duplicate.
2. If it can, that is stated where the retry hint is documented.
**Today:** ❌ missing
**Evidence:** `status.go:334` attaches `RetryInfo{1s}` to every `Unavailable`, and `DefaultRetryDelay` (`status.go:38`) calls it "the smallest honest hint that retrying is the right thing at all". A mesh — Envoy, Linkerd, or a grpc-go `retryPolicy` in the service config — retries `Unavailable` without being asked. `Create` is not idempotent, there is no idempotency key in any request document (`transport.go:98` builds every one of them and none carries a token), and `handler.go:157` passes the entity straight to the service. [[D-040]] says the framework does not retry on the caller's behalf, and `status_test.go:335` pins that with `TestTheFrameworkDoesNotRetryOnTheCallersBehalf` — but the hint invites somebody else to.
**If not ready:** Nothing here is wrong; what is missing is that the module invites the retry and never says the writes are unsafe under one. The default posture moving from HTTP to gRPC flips — nobody auto-retries a POST, and everything auto-retries `Unavailable` — so a team that moves a write path across doors inherits a duplicate they did not have. A sentence beside `DefaultRetryDelay` and in both errors sections is the minimum. Making the writes genuinely retry-safe is an idempotency key in the request document, which is a change to all four transports and a decision doc.

### H-CRUDGRPC-17 — The document changed and the far team did not know
**Who:** the platform team that reviews API changes
**Wants:** a change to the wire shape to be visible before it is deployed
**Story:** Someone adds a column, renames a `json` tag, and drops a field the reporting service still reads. The build is green, the tests pass, and the calling team finds out in production.
**Must hold:**
1. A change to what a resource sends or accepts is visible to the calling team before it ships.
2. There is a version, a descriptor diff, or a check somebody can run.
3. If the answer to both is no, that is one of the stated limits, in the consumer's own words.
**Today:** ❌ missing
**Evidence:** The wire shape is the model's own `json` tags (`message.go:15-24` says so explicitly, and it is the property [[D-052]] chose the Struct shape for). Nothing derives a descriptor from them, nothing versions them, and the four stated limits in `doc.go:38-61` and on both module pages do not name this one. `ServicePrefix` is `vv.crud.v1.` (`service.go:20`), which puts a `v1` in every method name that nothing ever increments.
**If not ready:** This is the strongest argument against adopting the binding, and it is the one the intro's "no review every time a column is added" reads as a benefit. For a service inside a mesh the review was a governance step: the schema registry, the platform team's API review and the backwards-compatibility CI most gRPC shops run all key on a descriptor this module does not produce. Rule 3 is the cheap half and does not wait on reflection: one paragraph in `doc.go`, `docs/modules/en/crudgrpc.md:188` and `docs/modules/ru/crudgrpc.md` — *there is no version and no compatibility check on the document; if you need one, generate a descriptor in your build* — which is what [[D-052]] already tells a consumer to do, in one clause, in a paragraph about grpcurl.

### H-CRUDGRPC-18 — I came here for throughput
**Who:** an analytics service pulling two million rows, and the architect who chose gRPC for the internal API
**Wants:** one call that streams rather than five thousand that page — and, failing that, a second door that is at least cheaper than the first
**Story:** They ask for the second port precisely because gRPC streams and because binary framing is supposed to beat JSON. They read the method table, find eight unary methods, and go looking for the streaming one. Then they benchmark a 1000-row page against the HTTP route it replaces.
**Must hold:**
1. Either `List` can stream, or the fact that it cannot is stated where the method table is.
2. Whether this door is cheaper than the HTTP one is stated at adoption time, not discovered in a load test.
**Today:** ❌ missing
**Evidence:** Rule 1: every method is `func(context.Context, *structpb.Struct) (*structpb.Struct, error)` (`service.go:82`), and `Desc` appends `grpc.MethodDesc` and never `grpc.StreamDesc` (`service.go:60-76`). The seam type is the limit, so this is structural rather than an oversight. It is stated nowhere: not in `doc.go:38`'s four limits, not in [[FL-013]]'s difference table, not on either module page. `StreamErrors` exists (`interceptor.go:53`) for the consumer's own streaming methods, which makes the absence easier to miss.

Rule 2 is unasked and the answer is very likely no. Every request makes a `protojson.Marshal` → `encoding/json.Unmarshal` hop and every response the reverse (`message.go:27-54`), so the JSON encode the HTTP door does once happens here too, with a protobuf encode added on top. And `google.protobuf.Struct` is a map: it carries a field *name* per value per row, so a 1000-row page is larger on the wire than the JSON it replaces, not smaller. That is the price [[D-052]] paid for one document across four transports and it is the right trade — but it makes this a compatibility and ergonomics story, not a performance one, and nothing says so.
**If not ready:** Today the export is five thousand unary calls, cursor-paged if the consumer discovers cursors exist. Server streaming is the one structural thing gRPC has that the three HTTP bindings do not, so a deferral is a defensible answer and silence is not. Rule 2 costs one sentence beside the four stated limits and prevents an adoption decision that cannot be undone once a service is built on it. Note that adding a streaming method later also changes the descriptor question H-CRUDGRPC-11 asks.

### H-CRUDGRPC-19 — Load fifty thousand rows in
**Who:** a job runner importing a nightly feed
**Wants:** the write side of the batch operation the read side already has
**Story:** They already call `BulkDelete` for the retention pass. Tonight's job has 50 000 new rows. They look for the matching method and find eight, five of which write one row at a time.
**Must hold:**
1. There is a way to write many rows in one call, or the fact that there is not is stated where the method table is.
2. If the answer is the repository rather than the wire, the document says which.
**Today:** ❌ missing
**Evidence:** The method table is eight, with `BulkDelete` and no bulk write (`doc.go:12-21`, `service.go:67-76`, `docs/modules/en/crudgrpc.md:33-42`). `remote/transport.go:22-26` declares `MethodCreate` and `MethodBulkDelete` and nothing between them, so the asymmetry is the port contract's and not this binding's alone. One layer down `crud.Repo` has `SaveAll` (`crud/repo.go:41-43`), covered by [[UC-008]], reachable in-process and not over either door. So a job runner makes 50 000 unary calls where the repository would have made one statement.
**If not ready:** Whether the answer is "a ninth method is coming", "use the repository directly, this door is for readers" or "never", a tag freezes the method table, and adding a ninth method later is the same descriptor and compatibility argument H-CRUDGRPC-17 makes. One sentence in the method table on both module pages settles the second rule at no cost.

### H-CRUDGRPC-20 — Prove my rules through this door before it ships
**Who:** the engineer who moved the reporting mount's presenter and scope onto gRPC
**Wants:** a test that calls the mounted resource the way a client will
**Story:** Their business rules are on the service value and tested there. The presenter, the scope, the locale and the status details are only observable at the door, so they go looking for the shape of a test that opens it.
**Must hold:**
1. There is a documented way to serve a handler in a test and call it, without a port, a generated stub or a database.
2. It survives being used twice in one test binary.
**Today:** ❌ missing
**Evidence:** The shape exists and is used by the library itself — `bufconn.Listen`, `grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(...), insecure.NewCredentials())` and a cleanup, twenty lines at `crud/rpc/crudgrpc/fake_test.go:214-233`, over an in-memory fake at `fake_test.go` — and is published nowhere: `grep -rn bufconn docs/ _examples/` finds one passing mention, in `docs/ai/usecases/modules/remote/UC-018-…:90`. There is no gRPC counterpart to [[UC-011]], whose own status is *partially covered* and whose gap 9 is that the handler test seam is not shipped. Rule 2 collides with H-CRUDGRPC-15 rule 3: the naive helper that mounts the same fixture under one name in two tests exits the test binary rather than failing a test (`google.golang.org/grpc@v1.83.1/server.go:788`) — the library's own helper avoids it only because each `serve` builds a fresh `grpc.NewServer`.
**If not ready:** Every team writes those twenty lines again, and gets them subtly wrong on a grpc-go version that moved the dialler options. A testing section on both module pages showing `bufconn` plus `crudtest`, with the one-server-per-test note, is the whole fix. A consumer who mounted their business rules on the second door and cannot test them through it will not trust the door.

### H-CRUDGRPC-21 — The mesh has to be able to route to it
**Who:** the platform engineer who owns the Kubernetes manifests
**Wants:** the things a gRPC service needs to be routable, or a sentence saying they are theirs to add
**Story:** They deploy the service. The readiness probe fails. They add the health service, and then it fails differently, because the auth interceptor refuses it.
**Must hold:**
1. A consumer finishing the mount page knows whether the two-line mount is the whole server.
2. If a health service, TLS credentials, keepalive and a max-connection-age are theirs to add, that is said once, where the mount is.
3. A health check is reachable behind the documented auth chain.
**Today:** ❌ missing
**Evidence:** `grep -rni "health" docs/modules/en/crudgrpc.md crud/rpc/crudgrpc/ _examples/pgx-grpc/` is empty, as are `tls`, `credentials` and `keepalive`. The mount snippet on both module pages is `grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors()))` and a `Register` (`docs/modules/en/crudgrpc.md:24-30`), which reads as the whole server. Rule 3's trap is known exactly one package over and appears nowhere a crudgrpc consumer reads: `authgrpc.Skip`'s own doc comment offers `/grpc.health.v1.Health/Check` as its example (`auth/rpc/authgrpc/interceptor.go:22`), and a server-wide `authgrpc.Unary(guard)` refuses the probe unless that name is listed. The failure is silent and total — the instance is marked unhealthy and gets no traffic, while every CRUD test the consumer wrote still passes.
**If not ready:** These are grpc-go's to provide and not this binding's, which is exactly why one paragraph saying so belongs here rather than nowhere: name `grpc.health.v1`, TLS credentials, `grpc.KeepaliveParams` and `MaxConnectionAge`, and show the `Skip` line the health probe needs. Four lines on two pages against a first-deploy outage.

## The DX this should have

The order below is the blocker table's, not the ergonomics one's: the items that
cost no API at all come first.

### The call site

```go
srv := crudgrpc.Reflecting(grpc.NewServer(          // *crudgrpc.ReflectingServer
    grpc.ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard)),
    grpc.ChainStreamInterceptor(crudgrpc.StreamErrors(), authgrpc.Stream(guard)),
))

crudgrpc.New(articles).Register(srv, "Article")
crudgrpc.New(comments).Register(srv, "Comment")

srv.Serve(lis)
```

Against today, which is three statements and three concepts:

```go
srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors()))
crudgrpc.New(articles).Register(srv, "Article")
srv.Serve(lis)
```

The mount line is identical in both, and that is the part to protect. What the
ideal adds is one wrapper call and one extra interceptor pair — still three
statements, still one line per resource. `Reflecting` is a **new symbol**;
`Errors`, `StreamErrors` and `Register` all exist.

`Reflecting` has one exact shape: `func Reflecting(*grpc.Server)
*ReflectingServer`. `ReflectingServer` embeds the supplied `*grpc.Server`,
implements `grpc.ServiceRegistrar`, overrides `RegisterService` to collect each
descriptor, and therefore still exposes `Serve`, `Stop`, `GracefulStop`, and
`GetServiceInfo` to ordinary grpc-go wiring. Generated
`crudgrpc.New(...).Register(srv, name)` calls the wrapper's registration method;
hand-written generated services can use the same `srv.RegisterService`. The
wrapper registers reflection from the collected descriptors before its embedded
server begins serving. `Reflect(descs...)` remains the escape hatch for a bare
`grpc.ServiceRegistrar` supplied by another framework.

`Reflecting` rather than `Reflect(srv, desc)`, and the difference is worth
arguing because the round-1 shape was worse. `Reflect(srv, h.Desc("Article"))`
forces the handler out of the chained mount into a variable, writes the resource
name a second time in the same file with nothing checking the two agree — the
identical two-string-literals failure H-CRUDGRPC-10 rule 4 calls "a green build
on both sides and `Unimplemented` at 3am" — and costs a line per resource. It
also cannot take a `grpc.ServiceRegistrar`: `reflection.ServerOptions.Services`
needs `GetServiceInfo() map[string]grpc.ServiceInfo`, which `ServiceRegistrar`
does not have, so the parameter would have to be narrowed and the seam `Desc`
exists to serve (`service.go:47-52`) would go. A registrar wrapper implements
`ServiceRegistrar`, accumulates each `*grpc.ServiceDesc` as it is installed,
makes ordering unrepresentable, and needs no name repeated. Keep
`Reflect(descs...)` as the escape hatch for a registrar this library did not
build.

`Errors` and `StreamErrors` should recover, and so should `unary`. The three
HTTP bindings put recovery in the middleware the consumer already installs
(`crud/http/crudnet/middleware.go:63-75`) because that is the only seam they
have. This binding has a better one: `unary` (`service.go:86-101`) is installed
by `Register` on every method unconditionally, and it wraps the decode hop at
`:90` that runs before any interceptor. Putting the `defer recover()` there
covers a consumer who never installed `Errors` and one who installed only the
unary half, which is what the module page's own snippet does. Keep it in
`Errors` and `StreamErrors` too, for hand-written methods. No new symbol, no new
dependency, no change to the call site. The same seam is where the `Internal`
gets logged — at `status.go:242`, before the status is built and the original
error is gone.

**The other end of the wire.** Every calling-side finding in this file has no shape to be measured against, so
here is one:

```go
conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))

articles := remote.New[Article, int64, ArticleInput](
    crudgrpc.Transport(conn, "Article",
        crudgrpc.WithVocabulary(codes),                              // the far side's own codes
        crudgrpc.WithCallOptions(grpc.PerRPCCredentials(forward{})), // the caller's token, per call
    ))
```

`forward{}` is a `credentials.PerRPCCredentials` whose `GetRequestMetadata`
reads the incoming caller's token off the ctx it is handed — the call's own ctx,
which is why two in-flight calls carry two tokens. Every symbol on this page
exists today. None of it is in any document, and `grpc.PerRPCCredentials` is
marked EXPERIMENTAL in grpc-go, which is worth one clause next to the snippet.

### Turning one knob

Two knobs, and they are different shapes.

The renderer is one value that should cover the whole server:

```go
rd := logging{next: crudgrpc.NewRenderer()}          // ten lines, delegates

srv := grpc.NewServer(grpc.ChainUnaryInterceptor(crudgrpc.Errors(crudgrpc.Using(rd))))
crudgrpc.New(articles, crudgrpc.WithRenderer[Article, int64, ArticleUpdate](rd)).Register(srv, "Article")
```

`Using(r Renderer) RenderOption` is a **new symbol**, but it must not become a
gRPC-only renderer precedence rule. The shared rule is Port's: process/server
renderer first; resource `Rendering(RenderOption...)` composes onto it while
retaining declared hops; an explicit replacement renderer changes only the
protocol body/status shape after Port-owned mapping. Crudhttp and Authhttp use
the same ownership split. Today `Errors` takes `RenderOption`s and the handler
takes a `Renderer`, so the second line above cannot be written at all — but the
value handed to `Errors` still never runs for a CRUD method, because the handler
renders first (`handler.go:312-313`) and the interceptor passes an already-
rendered status through (`interceptor.go:33-35`).

The version that actually delivers "one value covers the server" is different
and larger: `Errors` publishes its options on the context, and `build`/`fail`
compose them with the handler's own resolvers (`handler.go:78-84`,
`options.go:129-133`). That makes `Errors(WithMessages(catalogue))` mean what
both module pages already claim it means, and it is the only version where the
value is passed once. It is a behaviour change on all four bindings — `crudnet`
has the identical split at `crud/http/crudnet/handler.go:123-134` and `:477-479`
— and `doc.go:71-73` states the current split deliberately, so it needs a
decision, not a patch. Whichever is chosen, the precedence rule belongs in the
same change: the handler's renderer answers for the eight methods, the
interceptor's covers everything with no handler, and an error already carrying a
status is left alone so the double-install behaviour (`handler_test.go:516`)
survives.

The options are the other shape:

```go
o := crudgrpc.Opts[Article, int64, ArticleUpdate]()   // once per resource

crudgrpc.New(articles,
    o.WithQuery(cfg), o.WithScope(tenantOf), o.MaxBulk(100), o.WithTransform(present),
).Register(srv, "Article")
```

Against today's four calls each spelling `[Article, int64, ArticleUpdate]`, that
is six lines becoming seven and roughly 100 characters net — about 38 saved on
each of four lines, about 50 spent on the `o :=` line. The spellings stay
identical, so there is one name table and not two. `Opts` and not `For`: in
`NewFor`, `ServingFor`, `HandlerFor` and `Mapper[In, M]`, the *For* suffix
already means "with an input type of its own".

Costed at the twentieth mount rather than the first, the numbers are: `Opts` is
one line per resource, 15 in total, against a library cost of nine methods here
and roughly forty across four packages — whose option sets are deliberately
different, since `crudnet` has `MaxBody` and `WithErrorHandler` and this one has
neither. The alternative that needs no API at all is one declaration per option
per resource, 60 of them spread over eight packages at 15×4, each of which can
drift:

```go
var withQuery = crudgrpc.WithQuery[Article, int64, ArticleUpdate]
```

**On those numbers the proposal still does not pay**, and the `var` alias is the
recommendation. What both places a consumer reads show instead is a local helper
*function* per option (`crud/rpc/crudgrpc/options.go:33-42`,
`docs/modules/en/crudgrpc.md:94-97`), which is strictly more code for the same
result. Replacing those two snippets with the `var` form is the whole cheap
half, and the three HTTP bindings' option docs owe the same edit.

### Why this shape

The default has to be two lines, because the whole claim of this binding is that
a second transport costs almost nothing. It is two lines today, and every
proposal above leaves them alone.

Everything else is arranged so that reaching further is an argument added to a
line, never a different call site. Reflection is one wrapper on the server, not
a line per resource. A logging renderer is one value passed to two seams that
should speak one vocabulary. Recovery is invisible: it belongs inside the method
wrapper every `Register` already installs, so a consumer who never thinks about
panics gets the same answer as one who does.

What `Reflecting` actually does is worth writing down, because [[D-052]]'s
sixty-line estimate omits a step. `Register` builds a `*grpc.ServiceDesc` —
grpc-go's dispatch table, carrying no protobuf descriptor at all. So the work
is: synthesise a `descriptorpb.FileDescriptorProto` per resource, run it through
`protodesc.NewFile`, collect the results, and register the reflection service —
which D-052's sketch does not mention. The two functions the sketch names do
compose: `protodesc.NewFile` returns a `protoreflect.FileDescriptor` and
`protoregistry.GlobalFiles.RegisterFile` takes one, and grpc-go's reflection
server defaults `DescriptorResolver` to `GlobalFiles`
(`reflection/serverreflection.go:148-150`). The objection to `GlobalFiles` is
that it is process-global and errors on a duplicate name, which is fine at
start-up and hostile in a test binary that mounts the same fixture twice, as
`test/portmount` does. A private resolver avoids that, and costs a small wrapper:
`reflection.ServerOptions.DescriptorResolver` is a `protodesc.Resolver`, and a
`*protoregistry.Files` does not fall back to `GlobalFiles` for the
`google/protobuf/struct.proto` import on its own. Two more facts belong in the
estimate: `reflection.ServerOptions` and `reflection.NewServerV1` are both
marked EXPERIMENTAL (`serverreflection.go:104-107`, `141-147`), and `NewServerV1`
serves only the v1 reflection service, where `reflection.Register` also serves
v1alpha, which older grpcurl and evans builds still ask for. It may still be
sixty lines. It is not the sixty lines the decision describes.

### What it must not break

- [[D-052]] **forbids reflection outright in its Invariant sentence** — "no
  generated message per resource, no `protoc` in any build target, and no server
  reflection" — and repeats it as numbered decision 2. The deferral appears only
  under *Why*. So `Reflecting` is **a challenge to D-052 as written, not an
  implementation of it**: shipping it means amending the Invariant and decision 2
  in the same change, and rewriting `crud/rpc/crudgrpc/doc.go:46`,
  `docs/modules/en/crudgrpc.md:196`, `docs/modules/ru/crudgrpc.md`,
  `docs/usage-guides/gorm.md:782`, `docs/usage-guides/ent.md:844`,
  [[FL-013]]:113 and `_examples/pgx-grpc/main.go:16`. One of those is source.
  That is the real cost of the feature and it is not in the sixty lines.
- What D-052 refuses and reflection must keep refusing: **a per-resource message
  shape.** The descriptor describes the eight methods and their
  `google.protobuf.Struct` in and out, and nothing about the model.
- [[D-013]] owns "an unknown field is a rejection" for the query document and
  already rejected the forward-compatibility trade H-CRUDGRPC-14 would otherwise
  raise as new. Its **What it forbids** list says nothing about entity and patch
  bodies, so extending the rule to them is **an amendment to D-013, not an
  application of it** — and the `entity`/`ids` envelope keys are a smaller,
  separate change that needs no amendment at all, because an absent envelope key
  is a client mistake by every reading.
- **[[D-060]] page-cap authority is pending one explicit migration decision.**
  Today the physical `sqlrepo.MaxLimit` clamp is the only implementation
  (`crud/sqlrepo/blueprint.go:53-54`, `crud/options.go:241-252`), while the Query
  sweep proposes a route-owned `port.Rules.PageCap` and challenges D-060's old
  placement. Crudgrpc owns neither permanent surface: it must forward and test
  whichever owner the D-060 amendment selects, including the chosen relationship
  to `MaxLimit` and the Remote non-truncation rule. It must not document either
  current clamp or proposed field as the settled binding contract.
- [[D-063]] bounds **response** bodies as well as request ones, and names
  `ResourceExhausted` in both directions. The client's unclassified
  `*remote.ProtocolError` for an oversized page is a gap against it, and either
  the client classifies or the decision is amended.
- [[D-051]] and [[D-033]]: no new module dependency. `protodesc`,
  `protoregistry` and `reflection` are inside `google.golang.org/protobuf` and
  `google.golang.org/grpc`, both already required by this module.
- [[D-021]]: magic is allowed when it fails at start-up. A descriptor built at
  `Register` time does; one built on the first reflection request does not.
- [[D-045]]: one classification, spelled once per protocol. A renderer that logs
  must not re-derive a kind — it wraps `StatusRenderer` and delegates.
- [[D-062]]: nothing writes to a process-wide logger. The recovery and the
  `Internal` log both go through `port.Logger(ctx)` and nowhere else — which is
  also the argument for documenting a `port.WithLogger` interceptor, since
  without one the seam answers `slog.Default()` and the rule holds in letter only.
- [[D-044]]: the silence is on the wire. A server-side log line is not a
  disclosure, and the `Internal` status stays detail-free.
- CLAUDE.md's triplet rule does **not** propagate a change out of this module:
  `crud/rpc/crudgrpc` is named there as a fourth transport outside the triplet,
  and `Makefile:TRIPLETS` lists only the two HTTP triples, so `make
  check-triplets` cannot see this binding at all. Where a change here should land
  on all four anyway — the recovery, the renderer split, an option-surface change
  — the argument is design, not rule: shipping it on one binding re-creates the
  per-binding divergence `port.Rules` exists to end (`port/rules.go:16-18`).

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| Mount an existing service on a second transport | two lines, and the value is the same type — nothing to adapt | none |
| Mount fifteen of them | fifteen `Register` lines, one `Errors`, one auth chain — the split is clean, and no page shows it | small |
| Reads-only surface | one option — plus a second service name you have to invent, or grpc-go exits the process | small |
| Call it from another Go service as a repository | one line for the transport, one for the resource | none |
| Cursor-page a large table | works, and is named in no document or test on this transport | small, once you know |
| Forward each caller's token on an outbound call | `WithCallOptions(grpc.PerRPCCredentials(...))` — correct, undocumented, and grpc-go marks it EXPERIMENTAL | small to write, large to find |
| Bound the query on a service you built yourself | the option panics at start-up; the fix is `port.WithQuery`, documented on `port`'s page only | small to write, large to discover |
| Set an option | one 60-character call per option; the cheapest fix is a `var` alias nobody documents, and both places a consumer reads recommend a longer helper function | small to write, large to live with |
| Reuse a presenter or a hook across doors | four different function types, one per binding; two copies to keep in step and nothing comparing them | large |
| Refuse a stream from an unauthenticated caller | one interceptor that exists and is in no document | small to write, large to find |
| Survive a panic in a hook or a presenter | add a third-party recovery interceptor, or write one — a dependency this repository otherwise does not have | small to write, large to discover |
| One message catalogue for the whole server | the documented snippet is a no-op on all eight methods; the working shape is a `Renderer` per resource, plus the same options again on `Errors` | large |
| See why an `Internal` happened | implement `Renderer` (~10 lines), pass it per resource; the line then lands in `slog.Default()` with no method, no resource and no trace | large |
| Tell a caller they misspelled a field | nothing does, on either side, ever — and on `Replace` the typo blanks the row | large |
| Test the mounted resource before it ships | twenty lines of `bufconn` wiring, published nowhere, one server per test or the binary exits | large |
| Deploy it behind a readiness probe | health, TLS and keepalive are yours, and no page says so; the probe is refused by the documented auth chain | large |
| Load a batch in | eight methods, and the bulk one only deletes | large |
| Give the calling team a compatibility gate | generate a descriptor in their own build, over an undocumented shape | large |
| Poke at it with grpcurl | not possible as documented | large |
| Stream a large export | five thousand unary calls; the limit is stated nowhere | large |

**Overall:** For a Go-to-Go mesh with one resource on a good day this is close to
the ideal: the two-line mount is real, the eight methods hand over the same
commands the HTTP routes do, and the error contract is the strongest-tested part
of the module. What the sweep found is that almost everything else clusters
outside that description — at the twentieth resource, at the other language, at
the deploy, at 3am. Inside Go it gets wordy in exactly one place, and the honest
fix there is a `var` alias rather than the new API this file proposed in round 1.
Customising does not mean abandoning the short path — `New`, `NewFor`, `Serving`
and `ServingFor` all end in the same `Register` — but *observing* it does, and so
does *translating* it: the one snippet the module page gives for the error
contract configures a renderer that never runs, and the seam that does run is
per-resource and reaches nothing else on the server. That is the shape of most
of this table. The mount is right; the second thing you do after mounting is
where the module stops helping.

## Release blockers found here

| # | What | Severity | Lands in | Why it blocks |
|---|---|---|---|---|
| 1 | A missing or misspelled `entity` key on `Replace` silently overwrites the row with a zero model and answers success (`message.go:60-67`, `handler.go:209-221`, `port/service.go:190-212`) | blocker | `crud/rpc/crudgrpc` | Data loss from one typo, with a success on the wire. It is gRPC-only — an HTTP `PUT` body *is* the entity, so there is no envelope key to misspell — which makes it exactly the transport-specific hazard a tag would freeze. |
| 2 | A panic in a presenter, hook or renderer kills the process; the three HTTP bindings recover the same panic and this one recovers nothing — and `Errors` is optional, so a consumer following the module page's own mount is unprotected on streams as well | blocker | `crud/rpc/crudgrpc` | The docs claim every rule is identical across the four bindings. The one that differs turns a consumer's nil dereference into an outage, and [[FL-013]]'s difference table has no row for it. |
| 3 | `Errors(WithMessages(catalogue), WithCodes(codes))` — the only snippet either module page gives for the error contract — never runs for any of the eight methods, because the handler renders first and the interceptor passes a rendered status through | blocker | `crud/rpc/crudgrpc` + both module pages · all four bindings for the real fix | Half-working, which is worse than not working: a consumer ships untranslated sentences on every resource error and correct ones on their own methods, and nothing fails. Subsumes the `Errors`-takes-no-`Renderer` shape problem — same seam, same fix. |
| 4 | No descriptor, no reflection — and the example instructs the reader to use grpcurl, which cannot resolve a method without one | blocker | `crud/rpc/crudgrpc` + seven documents, one of them source | Anyone who opens a terminal is stopped on day one by an instruction that cannot work. |
| 5 | Nothing tells a caller it misspelled a field in an entity or a patch, and an absent `ids` on `BulkDelete` answers `{"deleted": 0}` where an absent `id` answers "missing id" | blocker | `crud/rpc/crudgrpc` + all four bindings, or an amendment to [[D-013]] | A create silently stores a blank column, an update silently writes nothing, and a nightly retention job reports success while deleting nothing forever. There is no schema on this transport, so no client can catch it either. |
| 6 | An `Internal` answer is silent on the wire *and* in the log, the original error is destroyed before any interceptor sees it, and no gRPC interceptor installs a request-scoped logger — so [[D-062]]'s seam answers `slog.Default()` | serious | `crud/rpc/crudgrpc` + both module pages | The one failure class the client is told nothing about is the one the server records nothing about, and the line that would fix it carries no method, no resource and no trace to tie it to the caller. |
| 7 | Eleven test names the three HTTP bindings each carry are missing here — five for `WithScope` including its asymmetry control, four for `BeforeSave`/`BeforeUpdate`, `TestWithTransformAppliesToWritesToo`, and `TestAllowClientIDLetsTheClientChooseTheKey` | serious | `crud/rpc/crudgrpc` | `make check-triplets` cannot see this module (`Makefile:TRIPLETS`), so nothing mechanical holds the parity. If the presenter stopped running on writes, a `Create` response would echo the columns it exists to hide and nothing here would fail. |
| 8 | An oversized page reaches the caller as a `*remote.ProtocolError` carrying no kind, where [[D-063]]'s Invariant names `errs.KindTooLarge` and `ResourceExhausted` in both directions | serious | `crud/rpc/crudgrpc` (`transport.go:235-246`) or an amendment to [[D-063]] | A decision doc is binding and this is a gap against one. A successful server answer arrives at the caller as an unclassified protocol failure, told from "the far side is read-only" only by the word in `ProtocolError.Status`. |
| 9 | Every `Unavailable` carries `RetryInfo`, a mesh retries it, and `Create` has no idempotency key | serious | `crud/rpc/crudgrpc` (the doc) · all four transports (a key) | The module invites the retry the writes are not safe under, and the posture flips silently when a write path moves from HTTP to gRPC. |
| 10 | No version, no descriptor diff, no compatibility check on the document — and it is not among the stated limits | serious | `crud/rpc/crudgrpc/doc.go` + both module pages | The governance step the `.proto` was doing is gone and nothing says so, so a platform team adopts an internal API with no compatibility gate believing they still have one. The disclosure is cheap and does not wait on blocker 4. |
| 11 | There is no bulk write on the wire: eight methods, `BulkDelete` and no counterpart, while `crud.Repo.SaveAll` exists one layer down | serious | `crud/rpc/crudgrpc` + `remote/transport.go` | A tag freezes the method table, and a job runner importing 50 000 rows makes 50 000 calls. Adding a ninth method later is blocker 4's compatibility argument again. |
| 12 | Nothing a consumer reads says a health service, TLS credentials, keepalive and max-connection-age are theirs — and `authgrpc.Unary(guard)` refuses `/grpc.health.v1.Health/Check` unless it is in `Skip` | serious | both module pages | A gRPC service with no `grpc.health.v1` is not routable by most meshes or Kubernetes probes. The failure is silent and total, and every CRUD test the consumer wrote still passes. |
| 13 | No documented way to test a mounted resource through this door; the twenty-line `bufconn` shape exists only in the library's own `fake_test.go` | sharp edge | both module pages · [[UC-011]] | The door is the only place the presenter, the scope, the locale and the status details are observable, and every team re-derives the harness — then hits blocker 14 when two tests share a name. |
| 14 | A second mount of the same resource name, or any `Register` after `Serve`, is grpc-go's `logger.Fatalf` — an `os.Exit` with a message naming nothing in this library | sharp edge | both module pages | H-CRUDGRPC-01's own story kills the process at start-up, and [[D-021]]'s argument about what a start-up failure looks like does not reach this one. |
| 15 | `context.Canceled` and `context.DeadlineExceeded` classify as `Internal` rather than `Cancelled` / `DeadlineExceeded` | sharp edge | `port/kind.go:110` + all four bindings | A client hanging up is recorded as a server fault, which is what an error budget is measured from. gRPC is the transport where it is first visible, because it has codes for both and HTTP does not. |
| 16 | `WithQuery` and `AllowClientID` panic at start-up when handed to `Serving`, and neither option table says so | sharp edge | both module pages | The two flagship cases of this sweep, followed in order, produce a process that will not start; the only page that mentions the panic is `port`'s. |
| 17 | `StreamErrors` is absent from this module's own errors section, and interceptor order is load-bearing and undocumented | sharp edge | both module pages | The `authgrpc.md` half of this is the authhttp sweep's row *"the documented gRPC stream wiring omits `crudgrpc.StreamErrors`, so a refused stream answers `Unknown`"* — one bug, two sweeps. This is the crudgrpc half, and reversing the chain breaks 401 on unary calls too. |
| 18 | `WithCallOptions` is documented as "per-call credentials" with no example, and the shape that delivers them (`grpc.PerRPCCredentials`) is EXPERIMENTAL in grpc-go; the far side redeploying arrives as a bare `ProtocolError` and the `RetryInfo` it sent is dropped | sharp edge | `crud/rpc/crudgrpc` + both module pages | The commonest mesh requirement works and is findable nowhere, and a consuming team cannot branch on "the far side is deploying" without string-matching. |
| 19 | Every option spells three type parameters, and the four bindings' `WithScope`, `WithTransform`, `BeforeSave` and `BeforeUpdate` have four different function types | sharp edge | all four bindings | A team on the twentieth resource keeps two copies of every presenter and every hook with nothing comparing them; a column hidden on HTTP and not on gRPC is a leak no test anywhere would catch. Costed above: not worth a new API, and the cheap half is documenting the `var` alias and a portable hook shape. |
| 20 | `List` is unary and cannot stream, and neither that nor the encoding cost of `google.protobuf.Struct` is stated anywhere | sharp edge | `crud/rpc/crudgrpc/doc.go` + [[FL-013]] + both module pages | Half the teams who ask for a gRPC door ask for throughput, and this one is very likely slower and larger than the JSON door it sits beside. That is an adoption-time fact that cannot be undone once a service is built on it. |

Blockers 1, 2 and 4 are on no roadmap: `docs/roadmaps/Roadmap.md`'s only gRPC
line is about a CI dependency gate. This sweep is discovering them, not
re-discovering them.

## Contested

- **Blocker 15 (`context.Canceled` → `Internal`) stays in this table**, against
  all three reviewers across two rounds, who place it on a `port`/`errs` sweep.
  The classifier is `port`'s and the *lands in* column says so, along with the
  fact that fixing it means a tenth `errs.Kind` and a row in crudhttp's status
  table. It stays because gRPC is the only transport with a code for both, so
  this is where a consumer sees the symptom, and a tag on this module ships it.
  Cross-referenced rather than owned.
- **Blocker 19 (three type parameters) stays**, against the code reviewer, who
  calls it double-counting against this module's tag. It is not this module's
  property — the *lands in* column says all four bindings — but a tag freezes the
  option surface, and this sweep is where it was measured. It is a sharp edge and
  the `Opts` proposal it used to carry is costed at fifteen resources and rejected.
- **H-CRUDGRPC-11 keeps its ❌**, though the round-1 framing was wrong in the
  consumer's disfavour: a non-Go caller needs no `.proto`, and the case now says
  so and prices the Python client at five lines. What is missing is the descriptor
  and everything built on it, which H-CRUDGRPC-17 turns into a governance argument
  with a blocker row of its own so the disclosure does not vanish behind the
  reflection work.
- **H-CRUDGRPC-11 and H-CRUDGRPC-17 stay separate cases**, against both
  reviewers, who read them as one absence split in two. They are one absence and
  two jobs with two different remedies: connectivity is fixed by shipping
  reflection, governance is fixed *today* by one paragraph that says there is no
  compatibility gate. Round 1 folded them into one blocker row and lost the cheap
  half; they now have rows 4 and 10.
- **H-CRUDGRPC-09 has been reused.** Round 1 spent it on "bound a service I
  assembled myself", which both reviewers called the thinnest case in the file.
  Its rules are now must-hold 5 of H-CRUDGRPC-02, where the job actually lives,
  and its blocker survives as row 16. The slot carries the presenter and the write
  hooks, which had no happy case at all.
- **H-CRUDGRPC-13 lost its cursor rule to H-CRUDGRPC-02** and its page-cap
  ownership complaint to the pending D-060 authority/migration decision. What is
  left is the two ceiling questions, and one of them is now a [[D-063]]
  conformance failure rather than a missing flow row. Crudgrpc tests and forwards
  the chosen shared owner; it does not choose one by documentation.

## Edge cases

### E-CRUDGRPC-01 — A native caller types an exact-looking 64-bit number
**Shape:** adversarial input
**Setup:** The primary key is `9007199254740993`, the first `int64` a `google.protobuf.Value` number cannot represent exactly.
**What the consumer does:** A hand-written native client sends that value as the numeric `id` in `Get`, as it would in JSON.
**What must happen:** The binding either addresses that exact row or refuses the numeric spelling and tells the caller to send an `id` string; it must never turn a key into its neighbour and answer it successfully.
**Today:** ❌ wrong or unhandled
**Evidence:** `message.go:94-110` accepts either scalar spelling, and `message.go:141-153` formats the already-rounded `float64`; `handler_test.go:228-256` deliberately proves that the numeric spelling does not reach the original key. The module page says keys are strings at `docs/modules/en/crudgrpc.md:200-204`, but the server still accepts the unsafe native form. No refusal test exists.
**Blast radius:** silent wrong answer

### E-CRUDGRPC-02 — `patch: null` is mistaken for an empty patch
**Shape:** misuse
**Setup:** A hand-written caller or a JSON-marshalling wrapper supplies `{"id":"42","patch":null}` rather than omitting the optional wrapper.
**What the consumer does:** They expect a malformed top-level patch document to be refused; explicit null remains meaningful *inside* a patch field and must not silently acquire a second, wrapper-level meaning.
**What must happen:** The binding rejects the null wrapper before the service is called, or gives it a documented, observable operation distinct from an empty patch.
**Today:** ❌ wrong or unhandled
**Evidence:** `message.go:60-73` returns nil for both an absent and a null nested document, `message.go:42-54` then leaves the zero DTO unchanged, and `handler.go:175-192` sends it to `Update`. `handler_test.go:168-224` pins null versus absent *inside* the nested object, not `patch: null`; no test exercises the wrapper value.
**Blast radius:** silent wrong answer

### E-CRUDGRPC-03 — The protobuf decoder rejects the request before the framework can render it
**Shape:** partial failure
**Setup:** A client sends a truncated or malformed protobuf frame for one of the eight unary methods.
**What the consumer does:** They need the malformed call to have the same deliberate, documented gRPC error contract as a bad document, rather than an arbitrary codec error that bypasses their server interceptors.
**What must happen:** The decode failure is either rendered as a stable client error or explicitly documented as a gRPC-layer exception to the error contract; it must be tested at the wire boundary.
**Today:** ❓ unverified
**Evidence:** `service.go:86-101` returns `dec(in)` directly before invoking either the handler or its interceptor; `interceptor.go:23-36` can therefore never render that error. The malformed-request test starts from an already-built `structpb.Struct` (`handler_test.go:258-270`), and no decoder-error test was found in `crud/rpc/crudgrpc`.
**Blast radius:** confusing error

### E-CRUDGRPC-04 — A zero violation cap means unlimited
**Shape:** misuse
**Setup:** A service author writes `NewRenderer(WithMaxViolations(0))`, reasonably reading zero as “use the published default”.
**What the consumer does:** One bulk-validation fault contains thousands of field violations.
**What must happen:** A non-positive cap is refused at construction or falls back to the advertised cap of 100; a one-character configuration mistake must not remove the response bound.
**Today:** ❌ wrong or unhandled
**Evidence:** `status.go:177-195` stores zero without validation, while `port/violations.go:9-16` describes the zero-value pipeline as having no cap and `port/violations.go:90-92` truncates only when `Max > 0`. `status_test.go:350-378` tests custom caps of 3 and 50 only; no non-positive-cap test exists.
**Blast radius:** crash

### E-CRUDGRPC-05 — The 100th and 101st invalid fields stay distinguishable
**Shape:** boundary
**Setup:** A batch validator returns exactly 100 field violations, then 101, with the default renderer.
**What the consumer does:** The caller renders every error at the limit, and when one is dropped it needs the `partial` marker before asking the user to correct a list that is known to be incomplete.
**What must happen:** One hundred entries arrive without `partial`; one hundred and one arrive as 100 entries with `partial=true`, in deterministic order.
**Today:** 🟡 partial
**Evidence:** `status.go:28-31` takes the 100-entry limit from `port.MaxViolations`; `status.go:251-267` passes it to the pipeline; `status.go:325-330` marks truncation. `status_test.go:350-378` establishes the behaviour only at 3/10 and 50/10, not the published 100/101 boundary.
**Blast radius:** confusing error

### E-CRUDGRPC-06 — A presenter returns something JSON cannot carry
**Shape:** degenerate declaration
**Setup:** A `WithTransform` function accidentally returns a channel, a function, or a value containing `NaN` after a successful create or read.
**What the consumer does:** They need a normal internal gRPC failure with no half-built document and no presenter value leaked to the peer.
**What must happen:** Encoding fails closed as an `Internal` status for every response shape, including a transformed page and a transformed write.
**Today:** 🟡 partial
**Evidence:** `handler.go:290-306` routes transformed values through `toStruct` and renders an encoding failure; `message.go:26-37` is the marshal/unmarshal boundary; `status.go:237-248` makes `Internal` detail-free. `handler_test.go:473-496` tests only successful transformed reads, so no test pins the failing presenter or the write shapes.
**Blast radius:** none

### E-CRUDGRPC-07 — One server changes the global locale keys while another serves calls
**Shape:** concurrency
**Setup:** A platform package appends a private metadata key to exported `LocaleKeys` during startup while another gRPC server in the process is already handling failures.
**What the consumer does:** They expect locale configuration to be fixed per server, or to fail loudly, not to race every request and make language selection depend on timing.
**What must happen:** The locale-key list is immutable after package initialization or copied into an explicit server configuration before serving begins.
**Today:** ❌ wrong or unhandled
**Evidence:** `locale.go:11-23` exposes `LocaleKeys` as a mutable slice, and `locale.go:34-49` iterates that same slice on every failed call with no synchronization or copy. `handler_test.go:565-605` covers one header at a time only; no mutation or concurrent-locale test exists.
**Blast radius:** confusing error

### E-CRUDGRPC-08 — Two locale sources disagree
**Shape:** seam
**Setup:** A gateway forwards `grpc-accept-language: fr`, a native sidecar adds `accept-language: de`, and an application interceptor has optionally installed `WithLocale(ctx, "ja")`.
**What the consumer does:** They need one documented priority rule, so a translated validation message does not change merely because a new proxy adds a header.
**What must happen:** An explicit locale wins; otherwise the metadata-key and repeated-value precedence is stated and tested with conflicting values.
**Today:** 🟡 partial
**Evidence:** `locale.go:25-49` gives an existing context locale priority and otherwise uses the exported key order and first parsable value. The module page names all three headers but no precedence (`docs/modules/en/crudgrpc.md:150-151`), while `handler_test.go:565-605` tests each key in isolation and the no-metadata fallback only.
**Blast radius:** confusing error

### E-CRUDGRPC-09 — A valid dotted or bracketed JSON key crosses the status carrier
**Shape:** Errs pointer | gRPC carrier seam
**Today:** ❓ the gRPC carrier currently writes `errs.Path.String()` into
`BadRequest.FieldViolation.Field` and parses it on return
(`crud/rpc/crudgrpc/status.go:294-300`, `transport.go:214-230`).
**Pointer:** `errs.Path.String` is deliberately lossy for dots/brackets
(`errs/path.go:109-119`), so Errs owns the lossless path-codec decision. Crudgrpc
must carry whichever stable string/segment representation Errs defines, and add
a separator-bearing transport control; it must not invent a second path grammar.

### E-CRUDGRPC-10 — A peer returns an enormous list of field violations
**Shape:** scale
**Setup:** An older or misconfigured remote service sends a framework-marked status with many `BadRequest.FieldViolations`, exceeding this binding's 100-entry outbound convention.
**What the consumer does:** The remote caller needs a bounded fault with an honest partial marker, just as it receives from a current server, rather than allocating one violation per received detail.
**What must happen:** The client applies `MaxViolations` while reconstructing remote failures and marks dropped detail as partial, or rejects the oversized status as a protocol error.
**Today:** ❌ wrong or unhandled
**Evidence:** The server uses the 100-entry cap (`status.go:28-31`, `:251-267`), but `transport.go:209-249` appends every received `FieldViolation` without a cap and returns them all in `FaultFrom`. `client_test.go:224-262` exercises one violation only; no oversized remote-detail test exists.
**Blast radius:** crash

### E-CRUDGRPC-11 — A client is constructed with a nil connection
**Shape:** misuse
**Setup:** A service's dependency wiring leaves `grpc.ClientConnInterface` nil, then constructs `crudgrpc.Transport(conn, "Article")` before the first call.
**What the consumer does:** They expect configuration to fail at construction with the resource name, rather than a nil dereference on the first live request.
**What must happen:** The transport fails loudly at the assembly boundary with a configuration error the caller can act on.
**Today:** ❌ wrong or unhandled
**Evidence:** `transport.go:34-42` stores `conn` without validation and `transport.go:76-92` unconditionally calls `t.conn.Invoke`. No nil-connection test was found in `crud/rpc/crudgrpc`.
**Blast radius:** crash

### E-CRUDGRPC-12 — One remote API instance serves concurrent requests
**Shape:** concurrency
**Setup:** A service shares one `remote.New(crudgrpc.Transport(conn, name))` value across hundreds of goroutines with different IDs, bodies and contexts.
**What the consumer does:** They expect per-call documents and errors never to bleed into another request, and a concurrency guarantee that is more than an accident of the current implementation.
**What must happen:** The transport remains safe for concurrent calls, with every request using only its own document, method and context, and a test makes that promise durable.
**Today:** ❓ unverified
**Evidence:** `transport.go:60-92` keeps the connection, service name and call options on the shared transport but builds `in`, `method` and `out` per call. No concurrent transport test was found in `crud/rpc/crudgrpc`; the client tests construct and use calls serially, for example `client_test.go:224-262`.
**Blast radius:** confusing error

### E-CRUDGRPC-13 — A native client cancels or reaches its deadline in flight
**Shape:** cancellation boundary
**Setup:** A native gRPC caller cancels a call after the server has entered the
repository, then repeats it with a short deadline.
**What the consumer does:** They expect the direct RPC result to be canonical
`codes.Canceled` or `codes.DeadlineExceeded`, and the server-side service/
repository context to become done rather than continuing detached work.
**What must happen:** The client context must pass unchanged to `Invoke`; the
server method must receive grpc-go's call context; cancellation/deadline must be
observed at both ends and pinned at the wire boundary. The Remote adapter must
not reduce either status to an unrelated internal fault.
**Today:** 🟡 partial
**Evidence:** `transport.Do` passes its caller context directly to
`t.conn.Invoke` (`crud/rpc/crudgrpc/transport.go:76-86`), and unary methods pass
grpc-go's context to their handler/service (`service.go:86-99`; handlers then
thread it to `h.svc`, e.g. `handler.go:143-147`). But a status with no framework
`ErrorInfo` becomes `remote.ProtocolError` carrying only `st.Code().String()`
(`transport.go:203-246`), and no cancellation/deadline integration control
proves the native status or server context completion.
**Blast radius:** abandoned work / confusing retry handling

## Edge verdict

The release-blocking wire defect is the canonical 64-bit numeric rounding path:
the server accepts a native number it has already rounded, then can act on a
neighbouring row. Path reconstruction is an Errs-owned codec decision carried by
gRPC, not a second Crudgrpc grammar. The binding does close a presenter
serialization failure and the normal violation cap by construction, but the
former lacks a failing-path test and the latter is removed by a zero
configuration. Its client half trusts remote error detail cardinality without
applying the server's bound; locale, decoder, and in-flight cancellation/deadline
contracts remain partly specified rather than pinned.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A native numeric `id` above 2⁵³ is accepted after the `Struct` has rounded it, so a successful `Get`, `Update`, `Replace` or `Delete` can address a neighbouring 64-bit key. | blocker | The client gets a successful operation on a row it did not name; the documented string route does not protect a caller the server continues to accept. |
