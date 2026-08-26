# crud/decorators/security — the rule that says whose rows these are, declared once next to the repository

**Covers:** `github.com/frostgrove/vv/crud/decorators/security`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — four blockers now write across a tenant boundary, empty a table, or turn an apparently gated repository into an unfiltered read: an empty effective predicate, a soft-delete overwrite, a zero policy, and a `Scope` that returns `nil` for an unrecognised caller. Of 39 cases 3 are ready, 18 are partial, 16 are wrong or missing and 2 are unverified. The statement-level enforcement is unusually well pinned, but declaration mistakes and the seams around it still fail late or open silently; the helper the module leads with serves one schema shape.

## What a consumer is actually trying to do

Somebody owns each row. A tenant, a customer, a team, a person. The author has
twenty endpoints and knows perfectly well what the rule is; what they cannot do
is write it twenty times and be right twenty times. They come here to say it
once, in one place, and stop thinking about it.

They want it to hold in the places they will not think about later. Not just the
list endpoint, which is the one everybody remembers, but the count that feeds the
pagination widget, the delete by id somebody guessed, the "select three, delete"
button on the list screen, the bulk update a background job runs at 3am, and the
comments hanging off an article that a `?preload=` reaches through a table the
original rule never mentioned.

Their schema is not uniform, and this is the part the advertisement skips. Some
tables carry the owner column; some do not, and reach it one hop away through
their parent. Some rows are reached only through a membership table — the
projects I am on, the documents shared with me. Some rows belong to everybody:
the system templates, the default categories that sit in the same table as the
customer's own. The key is an integer in one service and a UUID in the next, and
the owner is a user id about as often as it is a tenant id.

Ownership is also not always one value. A consultant belongs to three
workspaces. The value that decides which one this request is about is in the URL
— `/orgs/{orgId}/articles` — and the token says only which ones they are allowed
to ask for.

Underneath that there is a second, coarser question, and it is not the same
question: may this caller do this *kind* of thing at all. Readers do not delete.
The billing service reads and never writes. That is a decision that can be made
before a single row is fetched, and making it afterwards means the rows were
already read.

Then there is the awkward third layer they discover in week two: a rule that
needs the row in front of it. A locked document nobody may delete. A published
article its own author may no longer edit. No `WHERE` clause expresses that,
because the answer depends on data the query was going to return anyway.

And they want a caller who guesses an id to learn nothing, and to be told on the
day they wire it up that they got it wrong. A rule naming a column that was
renamed last sprint should stop the process from starting, not quietly narrow
nothing and read as protection for a year.

## Happy cases

### H-SECURITY-01 — Every endpoint of a tenanted resource, narrowed from the token
**Who:** the author of a B2B SaaS with `tenant_id` on every table and a tenant claim in every JWT
**Wants:** one declaration that covers reads, counts, writes and deletes, with nothing at any call site
**Story:** They mount the auth middleware, declare the rule beside the repository, and bind. Then they write the same handlers they would have written with no security at all.
**Must hold:**
1. The list, the total under it, the CSV export, the dashboard count and the existence check all carry the narrowing, in the statement.
2. Naming another tenant's row by id answers "not found", not "not allowed" — for every verb a client can send.
3. A partial update of another tenant's row writes nothing and answers "not found".
4. A delete by a guessed id removes nothing.
5. A row that leaves my scope between the check and the write is not written, and the caller is told not-found rather than handed back the refreshed row.
6. No filter, specification or second narrowing a caller can send widens it.
**Today:** 🟡 partial — (1), (3), (4), (5) and (6) hold; (2) has two exceptions.
**Evidence:** (1) is `TestScopeIsAppendedToEveryRead` (`security_test.go:63`) for the row reads and `TestCountAndExistsNarrowTheirSubqueries` (`relscope_test.go:198`) for the two scalar ones. The scope is *prepended*, so `Where` ANDs and cannot be subtracted (`security.go:225-238`), which is (6). (5) is `TestTheGateScopeIsInTheUpdatesOwnWhereClause` and `TestAnUpdateOfARowThatLeftTheScopeIsNotFound` (`gate_edge_test.go:76,100`) over `security.go:621-634`, whose comment names the check-then-act failure it comes from. `Aggregate` is overridden rather than inherited and the reason is written down (`security.go:353-361`); `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason` (`obligation_test.go:104`) fails the build if a thirteenth verb reaches the seam and nobody decides what the gate does with it, with `TestTheInheritedVerbsAreNotGated` (`obligation_test.go:156`) as its control so "inherited" means something. 404-not-403 on `GET`, `PATCH` and `DELETE`: `security.go:272-302`, `TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden` (`edge_test.go:157`).
The first exception to (2) is `PUT`. `saveTarget` probes for the row *without* the narrowing and answers `Denied(Update, "row is outside the scope")` (`security.go:539-547`), which the transport turns into 403. On an auto key the service pre-reads through the gate and answers 404 first (`port/service.go:191-195`); on a client-owned key — a uuid, a slug — `Replace` goes straight to `Save`, so `PUT /articles/{another-tenant-id}` answers 403 where `GET` on the same id answers 404. That is [[UC-004]]'s Gap 2, deliberate, traded for integrity. The second is bulk delete, which is not deliberate and is H-SECURITY-20.
**Cost, since the module reference does not state it:** `ScopeAttr` installs an `Inspect` (`policies.go:86-103`), so every `PATCH` pays a `SELECT` before the load-diff-write (`security.go:612-620`) and every `Save` carrying an id pays a lookup plus, on a miss, an `Exists` (`security.go:515-549`). `TestAPolicyWithNoPerRowRuleDoesNotLoadTheRow` (`security_test.go:371`) proves the *scope-only* policy is free and says so at `:390-391`; it is not the shape the advertised helper produces.
**If not ready:** nothing to write by hand for the `PUT` asymmetry — it is a decision. What is missing is one sentence in the module reference saying `PUT` answers 403 where `GET` answers 404, so a consumer does not find it in a pen test.

### H-SECURITY-02 — A create lands in the caller's own tenant without the client naming it
**Who:** the same author, on the first `POST` after the reads started working
**Wants:** the server to decide which tenant a new row belongs to, because the client is not a source of truth about that
**Story:** They post `{"title":"hello"}` from the front end. They expect the row stored against the tenant in the token — the same value the reads are already narrowed by. The next request is `PUT /articles/7` with the same body.
**Must hold:**
1. A create with no tenant field in the body is stored against the caller's tenant.
2. A create naming a *different* tenant is refused.
3. A whole-row replace that omits the tenant field keeps the row's own tenant rather than being refused.
4. Where the value comes from is declared in the same place as the narrowing, not somewhere else.
**Today:** 🟡 partial — (2) holds; (1), (3) and (4) do not.
**Evidence:** `ScopeField`'s `Inspect` compares the incoming row's field against the claim and denies on any difference (`policies.go:86-103`), pinned by `TestScopeAttrNarrowsInSQLAndFreezesTheColumn` (`principal_test.go:147`) with a control that a create into the caller's own tenant still succeeds. Nothing anywhere in the package writes to the model, so a zero `TenantID` is "a different tenant" and the create is a 403 reading `not allowed` — the reason is dropped at the wire (`port/kind.go:158`).
(3) fails through a different door and gives a different message. `Save`'s order is `saveTarget` → `authorize` → `Inspect`(existing) → `checkImmutableSave` → `Inspect`(incoming) (`security.go:469-501`), and `checkImmutableSave` compares stored against incoming (`security.go:553-578`). A `PUT` body with no `tenantId` reads as a *change* to a frozen field and is refused with `field TenantID is immutable`, pinned in the changed-value direction by `TestSaveJudgesAFrozenFieldByItsValue` (`edge_test.go:225`). The client is asked to echo a value it is forbidden to change — which the flagship example's own DTO comment argues against for the patch DTO (`_examples/auth-jwt-gin/main.go:68-70`), and that example demonstrates a list, a fetch-by-id and a delete and never a `POST` (`main.go:20-31`).
**If not ready:** `port.Mapper` is transport-neutral, takes the request context, and one implementation serves all four bindings — `Model(ctx, in) (M, error)` (`port/mapper.go:14-16`), called on create and on replace (`crud/http/crudnet/handler.go:298,356`; `crudgin:281,339`; `crudfiber:274,325`; `crudgrpc:162,217`). An input type with no tenant field plus a five-line `Model` that reads `auth.PrincipalFrom(ctx)` closes (1) and (3) at once, and `cmd/vv -adapter` already generates both to edit. What it does not close is (4): the tenant rule now lives in two files, it does not run for a write made from Go, and nothing at declaration says the second half is missing. Closing it properly is a hook the gate calls *before* `checkImmutableSave` — not a fourth closure in `Policy`, because `Inspect` runs on the wrong side of the freeze.

### H-SECURITY-03 — Readers read, editors write, only admins delete
**Who:** an author whose tokens carry roles that expand to permissions
**Wants:** one permission per verb, refused before any SQL
**Story:** They write the four-entry map, combine it with the tenant rule, and bind. A reader's `DELETE` is refused without the row being fetched.
**Must hold:**
1. The refusal happens before a statement runs.
2. A verb added by a future version of the library is refused by my existing map until I name it — `go get -u` cannot widen what my policy allows.
3. An unauthenticated caller is refused at every rule with nothing executed.
4. A permission list that comes back empty from configuration does not silently become "no rule".
**Today:** 🟡 partial — (1), (2) and (3) hold; (4) is deliberate and goes the other way for one of the two quantifiers.
**Evidence:** `PerAction` refuses an unnamed action outright and copies the map so a later write cannot change what is enforced (`principal.go:103-126`), pinned by `TestPerActionRefusesAVerbNobodyDeclared` (`principal_test.go:110`); that default-deny plus `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason`, which asserts `len(rec.Statements()) == 0` per verb, is (2). Every `Authorize` in `principal.go` calls `auth.Require` first — `principal.go:37,57,78,112`, and `InspectOwner` at `:213` — which errors on a context with no principal (`auth/context.go:45-51`); `TestEveryPrincipalPolicyFailsClosedWithoutOne` (`principal_test.go:247`) walks them.
(4): `RequirePermission()` with no permissions refuses nothing and `RequireAnyPermission()` refuses everything, both on purpose and both written down (`principal.go:31-33`, `:51-53`, `docs/modules/en/security.md:208-211`). A permission list built from configuration that comes back empty therefore produces, in the first spelling, a policy that authorises every verb — the fail-open direction, in the one layer whose job is to fail closed before any SQL. It does not reach the blocker table because it is documented twice and the composed policy's scope still narrows; it is here because the doc that explains it is not the doc a consumer reads while wiring config.
**If not ready:** the consumer checks the length of their own list, which is the check they came here to stop writing.

### H-SECURITY-04 — The support admin who has to see every tenant
**Who:** the author of the internal console, on the ticket that says "support cannot reproduce the customer's bug"
**Wants:** the same repository, the same policy, one role that is not narrowed
**Story:** They already have `ScopeAttr` working. They want to add: unless the caller is `platform-admin`, in which case no tenant filter — while keeping everything else the policy does.
**Must hold:**
1. Saying "this role is not narrowed" is an addition to the existing declaration, not a rewrite of it.
2. Turning the narrowing off does not turn off the frozen column.
3. The escape is visible in the declaration, so a reviewer can find every way the scope can be lifted by reading one place.
4. An admin reading one row by id gets the row, not a 403 — a 403 on a row is the shape [[D-008]] exists to prevent.
**Today:** 🟡 partial — none of it is available inside one policy; a second bind gives (2), (3) and (4) and costs (1).
**Evidence:** `Combine` only ANDs scopes; a sub-policy contributes a predicate or contributes nothing, and no combinator subtracts one (`policies.go:221-238`). The helpers never return a nil predicate: a claim the principal does not carry is a denial by design (`principal.go:175-187`), and `ScopeField` refuses a nil *value* outright rather than rendering `IS NULL` (`policies.go:54-61`). So "an admin returns nil", which `security.go:56-57` and `docs/modules/en/security.md:83` both recommend, is reachable only from a hand-written `Scope`. Grepping the package for any bypass, elevation or trusted-context concept returns nothing.
(4) is why the obvious wrapper does not work either. `ScopeField`'s `Inspect` compares on *every* action with no verb check (`policies.go:86-103`) and `GetByID` calls it unconditionally (`security.go:266`), so a wrapper that lifted only `Scope` would produce an admin who lists every tenant and gets 403 on the one row the ticket is about.
**The alternative, which round 1 rebutted with a false objection:** bind the blueprint twice — once with the tenant policy for the public router, once with `Combine(PerAction(…), Freeze[Article, int64]("TenantID"))` for the console. `Bind` is a pure function of blueprint, source and middleware (`crud/sqlrepo/blueprint.go:246-249`), so this works, and `security.Freeze` **is** the freeze without the scope: one line, exported, listed in the module reference (`policies.go:183-185`, `docs/modules/en/security.md:136`) and pinned by `TestFreezeRefusesAnUpdateThatNamesAFrozenField` (`gate_edge_test.go:211`), which binds a gate with a freeze and no scope at all and asserts both directions. So the console keeps its frozen tenant column and cannot re-file a row. What survives as a cost is (1) and (3): two repository values with nothing in the type distinguishing them, mounted on the right router by discipline, decided again for each of twenty resources.
**If not ready:** bind twice. The only shape that second bind cannot serve is one where the *same route* answers both callers, which is H-SECURITY-11 and H-SECURITY-23 — and those are a different missing thing, not this one. If a consumer hand-writes `Policy{Scope, Inspect, Immutable}` with an `if admin` in each instead, the moment they forget `Inspect` they are at [[UC-004]] Gap 1: reads narrowed, creates into any tenant open.

### H-SECURITY-05 — Columns nobody may edit through the API
**Who:** any author, on the first `PATCH` that could have moved a row between tenants
**Wants:** `tenant_id`, `owner_id` and `created_at` refused if a body names them
**Story:** They add the field names to the policy. A `PATCH` carrying `tenantId` is refused with the field named, before any SQL.
**Must hold:**
1. The refusal happens whether or not the value would actually have changed.
2. It holds through the whole-row verb as well as the partial one.
3. Both the field name and the column name are accepted as spellings, and they behave identically.
4. A name that matches nothing on the model stops the process from starting.
**Today:** ✅ ready
**Evidence:** the `PATCH` path compares against `crud.DefinedFields` before anything else (`security.go:585-596`), the whole-row path compares stored against incoming (`security.go:553-578`). Both speak the canonical spelling because `index` resolves every declared name once at `Gate` time and panics on one that resolves to nothing (`security.go:126-161`) — with the failure that motivated it in the comment: freezing `"tenant_id"` used to protect `PUT` and silently not `PATCH`. Pinned by `TestAFrozenFieldIsFrozenByEitherSpellingAndThroughBothVerbs` (`security_test.go:316`), `TestAFrozenFieldIsRefusedOnUpdateEvenWhenTheValueIsUnchanged` (`edge_test.go:205`) and `TestFreezingAFieldTheModelDoesNotHavePanicsAtDeclaration` (`security_test.go:353`).
**If not ready:** n/a. [[UC-004]] and `docs/ai/usecases/Index.md` both still list the unvalidated frozen name as an open gap; it is closed.

### H-SECURITY-06 — `?preload=comments` does not hand back the rows the rule exists to hide
**Who:** the author who exposed a relation on a tenanted parent
**Wants:** the far table narrowed too, including when the relation points at itself
**Story:** They add one line per relation path. The preload's second statement and a nested filter's subquery both carry the narrowing.
**Must hold:**
1. A preload, a nested filter, a count's subquery, an exists' subquery and a nested sort all carry it.
2. A path with a typo fails when the policy is declared, not on the request that leaks.
3. A caller cannot widen it by preloading with their own filter.
4. A path may be several hops, and a narrowing that follows a model wherever a hop lands on it — what a self-relation at depth needs — is expressible without leaving the helpers.
5. The narrowing I declare on the parent's blueprint and the one I declare on the parent's policy both apply to the same preload.
**Today:** 🟡 partial — (1) holds except for the nested sort, (2), (3) and (5) hold, (4) holds for a fixed path and not for a model.
**Evidence:** the relation narrowing is prepended alongside the scope on every read path (`security.go:204-238`), carried into `Delete(ids...)` after a bug where it reached `DeleteAll` and not `Delete` (`security.go:693-714`), and into a soft delete's `UPDATE`. Declaration-time resolution walks element types rather than tables, on purpose, so a policy declared as a package variable cannot cache a guessed table name (`policies.go:148-167`) — (2) is `TestABadRelationDeclarationPanics` (`relscope_test.go:305`) and (3) is `TestACallerCannotWidenARelationNarrowing` (`relscope_test.go:226`). `relscope_test.go` carries the positives *with controls*: `TestAPreloadIsNotNarrowedWithoutTheDeclaration` (`:102`) asserts the leak is there when nobody declares it, so the positive still means something. `TestEveryStatementAGatedCallIssuesCarriesTheNarrowing` (`:367`) covers the page total's `COUNT`, the soft-delete stamp and `Delete(ids...)`; `crud/options.go:231` is why the total is narrowed. [[UC-004]]'s Gap 4 — a page total counted over a wider set — is closed and pinned, and neither the use case nor the index says so.
(5) is stated carefully because round 1 stated it backwards, in the direction that leaks. A blueprint's narrowings belong to the repository issuing the statement, not to the model being reached: every statement folds `r.bp.relScopes` — *this* repository's own blueprint — with the request's (`crud/sqlrepo/repository.go:127-134`), and nothing consults another model's blueprint. Gating the `/comments` repository does nothing for `/articles?preload=comments`. What holds is `sqlrepo.RelationScope("Comments", …)` on the **Article** blueprint composing with the **Article** policy's `ScopeRelationField`, which is `TestARelationScopeStillAppliesUnderAGate` (`gate_edge_test.go:237`) and [[UC-004]] guarantee 12. `crud/sqlrepo/blueprint.go:82-84` says it in the doc comment: reaching a different model is another repository's business.
The two holes: `grep -n "Sort\|OrderBy" crud/decorators/security/*_test.go` returns nothing, so no test covers a *policy-supplied* narrowing reaching a nested sort's subquery — the only proof is `crud/sqlrepo/relscope_test.go:95`, built on the blueprint's table-level scope, and `docs/ai/usecases/Index.md` gap 11 records the difference. And both relation helpers only ever build `AtPath` (`policies.go:126-136`); the by-model form is `crud.RelationScopes.ForModel` (`crud/scope.go:76`), reached by no constructor and no test here, so a self-relation means hand-writing `RelationScopes` — which is what `docs/modules/en/security.md:114-121` tells you to do, with two `AtPath` calls that narrow `Children` and `Children.Children` and nothing deeper.
**If not ready:** for the sort, a test; if it fails, the same bug as the four in `TestEveryStatementAGatedCallIssuesCarriesTheNarrowing`. For the self-relation, a `ScopeModelField[M, T]` beside the two path helpers, three lines over `ForModel`.

### H-SECURITY-07 — A locked row nobody may delete
**Who:** the author of a document service, adding a lock
**Wants:** a rule that sees the row
**Story:** They add a per-row check that refuses `Delete` for a locked document. It fires whether the delete named the id or matched a filter.
**Must hold:**
1. The check is told which verb is being attempted.
2. On a filtered write, every row about to change is inspected — an id in the call is not the only thing that can stand for consent.
3. A projection cannot dodge it: a caller who does not select `locked` does not get a zero value believed.
4. A page containing one row the check refuses fails rather than silently returning the rest.
5. Turning the check on for reads does not remove an endpoint that was working.
**Today:** 🟡 partial — (1), (3) and (4) hold; (2) is false in one reachable case and (5) is false.
**Evidence:** `UpdateAll` and `DeleteAll` read the victims and inspect each before writing (`security.go:663-673`, `727-737`), pinned by `TestUpdateAllInspectsEveryRowItIsAboutToWrite` (`updateall_test.go:117`). The projection is cancelled for any read whose rows will be inspected, with the two-directional failure written down at `security.go:240-252`; `TestAProjectionCannotBypassAnInspectRule` and `TestAProjectionDoesNotTurnEveryScopedReadIntoADenial` (`gate_edge_test.go:140,122`) are the pair. Veto-not-trim is `TestInspectReadsFailsThePageInsteadOfTrimmingIt` (`edge_test.go:338`). `InspectOwner` is told the verb — `TestInspectOwnerIsToldWhichVerbIsBeingAttempted` (`principal_relscope_test.go:117`).
(2) breaks on a caller-supplied `crud.Limit`. The victim reads pass the caller's options through (`security.go:664`, `:728`); `repository.GetAll` honours a limit whenever any paging option is present (`crud/sqlrepo/repository.go:270-284`) while `UpdateAll` and `DeleteAll` emit no `LIMIT` (`repository.go:834-871`, `903-918`). So `Limit(1)` on a gated filtered delete inspects one row and deletes every match. That is [[D-026]], `Status: open`, which names three ways to settle it and states that "assume `Inspect` has seen every row a gated filtered write touched" is the assumption that does not hold. No test covers it, and D-026 says writing one is the first step. Not reachable from HTTP — no binding exposes either verb.
(5) is `InspectReads` plus `Aggregate`. With any `Inspect` set — and `ScopeAttr` always sets one — switching `InspectReads` on makes every `Aggregate` return `Denied` outright (`security.go:366-368`), so the dashboards, the group-by counts and the CSV summary answer 403 with a reason the wire drops. `Combine` ORs the flag (`policies.go:217`), so one sub-policy switching it on does it to the whole policy. No test covers the refusal; `test/integration/aggregate_test.go:157` covers the narrowed case only.
**If not ready:** for (2), pick one of D-026's three options — option 3, refusing paging options on `DeleteAll`/`UpdateAll` in the repository, removes the ambiguity rather than working around it. For (5), the refusal should say which knob caused it, or `Aggregate` should be allowed when the policy declares it has nothing to check. Separately, a per-row rule that has to ask the database — "may edit if they are a member of this project" — is a Go call per row (`security.go:663-673`), so it is N+1 on every filtered write and on every list once `InspectReads` is on. Nothing in the package says so; H-SECURITY-24 is the shape that avoids it.

### H-SECURITY-08 — The 3am cleanup job that empties one tenant's archive
**Who:** the author of a background worker, with no HTTP request anywhere in sight
**Wants:** a filtered delete that is narrowed, and a loud refusal if it ever is not
**Story:** They call the filtered delete with an archive predicate. Later somebody writes "delete everyone not in this active list", and the list comes back empty.
**Must hold:**
1. A filtered delete or update with no *effective* narrowing from either the policy or the caller is refused.
2. Allowing an unscoped delete does not allow an unscoped update.
3. Saying "yes, I mean the whole table" is one obvious edit, and it survives being composed with the rest of the policy.
4. A worker with no request behind it can run under the same policy, and how it says who it is takes one line.
5. The memory the job uses does not scale with the number of rows it deletes.
**Today:** ❌ (1) is false in the shape that occurs; (2) holds; (3) has a trap; (4) works and is undocumented where a consumer looks; (5) is false.
**Evidence and the failure:** the guard is a **nil test on the predicate** — `if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedDeleteAll` (`security.go:724`, and `:660` for `UpdateAll`). A tautology is not nil. `crud.NotInAny` over an empty slice renders `1 = 1` (`crud/predicate.go:200-209`) and so does an empty conjunction (`crud/predicate.go:349-357`), so `DeleteAll(crud.Where(crud.NotIn("ID", stillActive...)))` with an empty `stillActive` walks through the guard carrying `WHERE 1 = 1`. Under `ScopeAttr` the scope still ANDs, so the blast radius is one tenant's entire table; under a `PerAction`-only or `Freeze`-only policy — a normal composition this file treats as normal elsewhere — it is `DELETE FROM "articles" WHERE 1 = 1`. `docs/ai/usecases/modules/specs/Specs.md:148` already writes the counter-example down against this exact line and observes that two independent layers exist to stop it and the same input walks through both. `grep -n "NotIn\|1 = 1" crud/decorators/security/*_test.go` returns nothing.
(2) and the nil case do hold: the two guards are separate (`security.go:660-662`, `724-726`), pinned by `TestUnscopedDeleteAllIsRefused` (`security_test.go:213`) and `TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt` (`updateall_test.go:41`). (3): `Combine` starts both flags at `len(ps) > 0` and ANDs each policy's, so a `Combine` containing any helper-built policy — every helper leaves them false — turns a granted permission back off (`policies.go:197,218-219`). `TestCombineOfNothingIsNoMorePermissiveThanTheZeroPolicy` (`gate_edge_test.go:184`) pins the empty case, which is the right answer; nothing pins the composed case.
(4): the principal-driven helpers call `auth.Require` first (`principal.go:37,57,78,112,177,192`), so under `ScopeAttr` a cron job executes no statement until it puts a principal in its own context. `ctx = auth.WithPrincipal(ctx, auth.Claims{Sub: "cron", Attrs: map[string]any{"tenant": id}})` is the whole answer and is graded in `docs/ai/usecases/general/General.md` H-GENERAL-13; the line itself is shown in `docs/modules/en/auth.md:105`. What is security's own half is that `ScopeAttr` makes it *mandatory* rather than optional and `docs/modules/en/security.md` never says so — no guide, no example and no usage guide builds a principal outside a request.
(5): because `ScopeAttr` sets `Inspect`, `DeleteAll` reads every matching row — whole rows, projection cancelled by `whole` (`security.go:247-252`, called at `:728`), on the primary — into a slice before the `DELETE` runs. A cleanup job over a large archive is an out-of-memory, not a slow query, and the `Limit` that would bound it is the one (2) documents as ignored by the statement. [[D-026]] names this as the cost of its own option 1.
**If not ready:** for (1) the consumer checks their own slice for emptiness before calling, which is the check the guard was supposed to be. Closing it needs `crud` to answer "is this predicate unconditionally true", which [[D-003]]'s closed AST makes possible and [[D-054]] shows the shape for — and the same change closes `specs.DeleteBy`. For (3), `p := security.Combine(...); p.AllowUnscopedDeleteAll = true` works, is two lines, and appears in no document: `Combine`'s doc comment states the AND rule (`policies.go:187-190`) and never mentions it, and `grep -rn "AllowUnscoped" docs/` finds only the field defaults. Setting it on a sub-policy is what a consumer tries first and silently does nothing. For (5), a batched victim read.

### H-SECURITY-09 — The nightly sync that upserts ten thousand rows
**Who:** the author of an integration worker, importing from a partner API under the same tenant policy
**Wants:** the batch to be checked and still be one statement
**Story:** They call the batch save with rows carrying ids from the previous sync. It has to be safe and it has to finish before morning.
**Must hold:**
1. Every row in the batch is checked, not just the first.
2. A row addressed at an id in another tenant is refused rather than re-tenanted.
3. The cost is proportionate to what was asked for.
**Today:** 🟡 partial — (1) and (2) hold; (3) does not, and the code says so.
**Evidence:** `SaveAll` is spelled out rather than inherited precisely because the call that writes the most rows would otherwise check none (`security.go:412-458`), and `TestSaveAllIsCheckedByTheGate` (`test/integration/saveall_test.go:132`) refuses a two-row batch on the **second** row and carries the control that an all-mine batch goes through. The overwrite check runs a lookup and a second `Exists` when nothing visible came back (`security.go:515-549`), so a batch of upserts under any policy with a scope costs one to two statements per row before the single `INSERT`; the comment at `security.go:410-411` states the price plainly. The principal problem is H-SECURITY-08 (4) and the same one-line answer.
**If not ready:** they split the batch by "has an id" and accept the cost on the upsert half, or move the sync to a repository bound without the gate and take responsibility for the narrowing by hand. A batched overwrite check — one `IN` over the batch's ids instead of N lookups — is the obvious close and changes no refusal.

### H-SECURITY-10 — Proving the policy is right, in a unit test, with no database
**Who:** any author, writing the test that stops the rule from being deleted by a refactor
**Wants:** to assert what SQL a gated call produced, and what it refuses, in a normal `go test`
**Must hold:**
1. A coarse rule can be checked with no repository at all.
**Today:** ✅ ready
**Evidence:** `Policy`'s fields are exported function values, so `policy.Authorize(ctx, security.Delete)` and `policy.Scope(ctx)` can be called directly, with no recorder and no repository, which is how a permission map is tested in three lines. The statement-level half — asserting the `WHERE` and asserting a refusal issued nothing — belongs to `crud/crudtest` and is graded as H-CRUDTEST-07 in `docs/ai/usecases/modules/crudtest/Crudtest.md`, whose evidence line cites this package's four test files.
**If not ready:** n/a.

### H-SECURITY-11 — Public reads, authenticated writes
**Who:** the author of a public API with an authenticated admin surface over the same table
**Wants:** anonymous callers to read published rows and nothing else; authenticated callers to write their own
**Story:** They mount the optional guard so a request with no credential proceeds anonymously, and expect the policy to answer "published only" for that caller.
**Must hold:**
1. An anonymous caller is a caller, not an error, where the author says so.
2. The narrowing for an anonymous caller is still in SQL.
3. Saying it does not mean giving up the principal-driven helpers for the authenticated half.
**Today:** ❌ — the transport half ships; the policy half has nothing to say, and (3) is where it bites.
**Evidence:** the transport half exists and is honest about what it does not do: `auth.Optional()` lets a request with no credential through unauthenticated and still refuses a *bad* one (`auth/guard.go:64-78`), and its doc comment states the consequence — "an optional guard in front of a gated repository is a 401 at the repository instead of at the door, not an open door" (`auth/guard.go:72-75`).
The split is narrower than round 1 said, and the correction matters for the sizing. `policies.go` never imports `auth` — its import block is `context`, `reflect`, `strings` and `crud` — so `ScopeField`, `ScopeRelationField`, `ReadOnly`, `Freeze` and `Combine` all compose with an anonymous request. What an anonymous caller costs is the *principal-driven* half: `ScopeAttr`, `ScopeRelationAttr`, `ScopeSubject`, `PerAction`, `RequirePermission`, `RequireRole` and `InspectOwner` each call `auth.Require` (`principal.go:37,57,78,112,177,192,213`), and `Combine` returns the first error from its scope chain and from its authorize chain (`policies.go:221-237`, `:250-259`), so one of them in the `Combine` refuses the anonymous request outright.
(2) also cannot be reached by the shape a reader will reach for. What an anonymous caller needs is a *different, narrower* predicate — published only — and nothing in the package substitutes a scope. A combinator that lifts hooks would produce an anonymous caller who reads everything, which is the opposite of the case.
**If not ready:** a hand-written `Scope` closure branching on `auth.PrincipalFrom` returning false — not on a null principal, because `Optional` invents none — roughly ten lines, plus `security.Freeze` and `security.ReadOnly` which still compose. What has to be rewritten by hand is every principal-driven helper the authenticated half wanted. The missing thing is a combinator that *substitutes* a scope on a context predicate, which is not the same combinator H-SECURITY-04 wanted and is the one H-SECURITY-23 needs too.

### H-SECURITY-12 — "Why did that customer get a 403?"
**Who:** whoever is on support duty
**Wants:** the reason in the service's own logs, and nothing extra on the wire
**Story:** A ticket says a `PATCH` returns `not allowed`. They go looking for which rule refused it.
**Must hold:**
1. The client learns nothing beyond the status.
2. The operator can find out which rule fired, without adding a `log.Printf` to the library.
**Today:** 🟡 partial — (1) holds; (2) costs a seam that belongs to another module.
**Evidence:** `Denied(action, reason)` keeps the reason in the Go error (`security.go:178-181`) and nothing in this package consumes it — every `port.Logger` call site in the repository is a panic, an encoding failure or a detail-attachment failure, none a refusal ([[D-062]]). That is security's whole half, and it is the right half. The drop is `port/kind.go:158`, which synthesises a forbidden fault with an empty reason and a nil path, so the body is the generic `"not allowed"` from `errs/codes.go:91` — [[UC-020]]'s guarantee 10, and it holds.
**If not ready:** the seam is `porthttp.Renderer`, [[D-059]]'s territory, and `docs/ai/usecases/modules/port/Port.md` carries it in detail — its blocker 3 is that nothing installs a `Renderer` except per resource, and its blocker 2 is that installing one per resource silently drops the generated path map (`crud/http/crudnet/handler.go:126-133`). The remedy is a dozen lines per resource and it costs the path map. **The blocker is on port/porthttp's row and deliberately not on this module's**, so the owner does not count it twice.

### H-SECURITY-13 — The comment table that has no tenant column
**Who:** the same B2B author, exposing `/comments` as its own resource
**Wants:** comments narrowed by the tenant of the article they hang off
**Story:** Comments carry `article_id` and nothing else — the tenant is one hop away, on `Article`. They try the same one-liner with a dotted field name and expect it to work the way a filter does.
**Must hold:**
1. A rule whose column lives one hop away is declarable.
2. Declaring it brings the same create check and frozen column the flat case brings.
3. A typo in the path fails at declaration.
**Today:** ❌ missing
**Evidence:** `ScopeField` resolves the field with `Schema.Field`, which looks up by Go name, by column and by a fold that strips `_`, `-` and space and nothing else (`crud/meta.go:113-129`, `:145-156`) — a dot resolves to nothing, so `security.ScopeAttr[Comment, int64]("Article.TenantID", "tenant")` panics at declaration with "model Comment has no field Article.TenantID" (`policies.go:32-36`). The predicate layer has no such limit: `crud.Eq("Article.TenantID", v)` renders as a correlated `EXISTS` over `articles` (`crud/predicate.go:84-120`), a correct narrowing. `ScopeRelationAttr` does not help — `RelationScopes` narrows the hops a query walks, not the statement's own `FROM`, which is what `Scope` is for (`security.go:56-63`).
**If not ready:** they hand-write `Policy{Scope: func(ctx) { return crud.Eq("Article.TenantID", t), nil }}` — which narrows every read correctly and comes with no `Inspect` and no `Immutable`, so a create posting a `Comment` onto another tenant's article is unconstrained. That is [[UC-004]] Gap 1 exactly, reached by a consumer following the documented shape. This is the second most common table in a tenanted schema. Closing the read half is letting `ScopeField` accept a path when the model has no such column; the create half needs the row's parent, which is a lookup the policy cannot do in Go and probably wants `Immutable` on the foreign key plus a check at the parent instead. The bundled `Inspect` cannot follow a dotted name at all — `schema.Values` has no local field to read — so the honest statement is that the seam closes the read half and leaves Gap 1 open here.

### H-SECURITY-14 — Rows that belong to everyone
**Who:** any author whose table holds system templates or default categories alongside the customer's own
**Wants:** "my tenant's rows, plus the ones with no tenant"
**Story:** They add a row with a null `tenant_id` and expect every tenant to see it, without editing twenty policies.
**Must hold:**
1. "Mine or shared" is expressible without leaving the helper.
2. Whatever expresses it keeps the create check: a caller may not create a shared row.
**Today:** ❌ missing
**Evidence:** `ScopeField` compiles exactly one predicate — `crud.Eq(f.Name, v)` (`policies.go:84`) — and refuses a nil value outright rather than rendering `IS NULL`, with the reason in the comment: a nil would turn the documented "an admin returns nil" reading into an empty page and a denial of every create (`policies.go:55-61`). There is no `ScopeFieldOr`, no predicate parameter, and no way to reach the value the helper computed.
**If not ready:** hand-write `Scope` returning `crud.Or(crud.Eq("TenantID", t), crud.IsNull("TenantID"))` — and lose the bundled `Inspect` and `Immutable` again. Four separate real scenarios now end at "abandon the helper" (this one, H-SECURITY-13, H-SECURITY-19, H-SECURITY-24), which changes the diagnosis: the helper does not have a gap, it compiles one rule shape. A constructor taking `func(value any) crud.Predicate` beside `ScopeField` would cover all four read halves in one addition. Be precise about what it does *not* fix: the bundled `Inspect` stays strict equality (`policies.go:99-101`), so under "mine or shared" every shared row the caller can now read 403s on update. For this case that is arguably the right answer and should be defended as such, not described as merely surviving.

### H-SECURITY-15 — The claim's Go type and the column's
**Who:** any author whose ids are UUIDs, whose tenant column is text, or whose owner column is a `bigint` — which is most of them
**Wants:** the same one-liner, whatever the two types happen to be
**Story:** They write `security.ScopeAttr[Article, uuid.UUID]("TenantID", "tenant")`, it compiles, the process starts, and the first request panics. Or they write it against a `varchar` tenant column with a numeric claim, it compiles, it starts, and every user sees an empty application.
**Must hold:**
1. A claim that cannot be compared with the column — or that *converts to something else* — is caught before the process serves traffic.
2. A tenant claim the issuer nested — `{"org":{"id":"…"}}` — is reachable.
**Today:** ❌ missing for both, and the second direction is the worse one because it is silent.
**Evidence:** names are resolved at declaration; value types are not. `reconcile` runs inside the `Scope` and `Inspect` closures (`policies.go:54-72`, called from `:75-84` and `:86-103`), so a type that is not `ConvertibleTo` the column's panics on the first request — `TestAnUncomparableClaimTypePanicsRatherThanDenyingEverything` (`policies_test.go:72`) proves the panic and its comment says where: "The panic is at first use, which is where the extractor is first called" (`:81`). A JWT claim decodes to `int64` or `string` (`auth/authjwt/claims.go:64-95`), and `string` is not convertible to `uuid.UUID` or `pgtype.UUID`. With `crudnet.Errors` mounted that is a logged 500 per request (`crud/http/crudnet/middleware.go:63-75`).
**The silent direction has no test and no panic.** `reconcile` converts whenever `got.ConvertibleTo(want)` (`policies.go:66-67`), and an integer **is** convertible to a string in Go — as the rune conversion. `narrow` turns an integral `json.Number` into `int64` (`auth/authjwt/claims.go:64-71`), so a `"tenant": 42` claim against a `tenant_id varchar` column compiles to `WHERE tenant_id = '*'`, and stays there. No panic, no error, an empty application for every user and nothing in the logs. `policies_test.go` covers the width case and the incomparable case; nothing covers converts-to-nonsense.
(2) fails earlier and more quietly still: `attrOf` reads one flat claim name (`principal.go:175-187`), and a nested claim comes back as a `map[string]any`, which is not convertible to anything and panics.
The near-miss is on record: the comment at `policies.go:36-51` says the shipped gorm guide's own `ScopeAttr[Member, uint]("TenantID", "tenant")` against an `int64` claim was a working reproduction of a third variant — reads fine, every create denied — and `TestAClaimOfADifferentWidthThanTheColumnStillWorks` (`policies_test.go:38`) closed it. The width case was closed; the type case was left as a panic at first use, and the rune case was opened by the fix.
**If not ready:** they stop using `ScopeAttr` and use `ScopeField` with an extractor that parses the value, which is five lines and correct. Closing (1) means resolving the value type at declaration, which the extractor's `any` signature does not allow — so either a typed constructor (`ScopeAttrAs[M, ID, V]`) or, at minimum, refusing the integer-to-string conversion by name and documenting the rest. [[D-021]] is explicit that the magic must fail at build or start-up and never at request time; this is the one place in the package that does not.

### H-SECURITY-16 — Two writes in one transaction, both gated
**Who:** any author whose service does more than CRUD
**Wants:** an order and its lines written together, both checked, and rolled back together
**Story:** They open a transaction on the gated repository and write both models inside the closure.
**Must hold:**
1. The transaction itself is not refused for an action nobody took.
2. Every write inside it is checked exactly as it would be outside.
3. The gate's own extra reads — the load before an update, the overwrite probe, the victim read — see the uncommitted rows, not the pool.
**Today:** 🟡 — all three are true by reading, and only (1) has a test that could fail.
**Evidence:** `Tx` is one of the two verbs the gate deliberately inherits, with the reason in the obligation table: the closure reaches the database through the same gated repository, so a gate of its own "would refuse the transaction rather than the work, which is a denial for an action nobody took" (`obligation_test.go:87-93`), and `TestTheInheritedVerbsAreNotGated` (`:156`) is the control that it passes through. (3) follows from the gate calling `g.Core` for its own reads and the repository preferring a context executor over its own source, in both `exec` (`crud/sqlrepo/repository.go:95-100`) and `read` (`:109-116`) — `read`'s comment states exactly this property, that joining a transaction and then reading around it would defeat it.
The whole of the `Tx` coverage in this package is `obligation_test.go:169-177`, a closure that sets a boolean and writes nothing; it would pass unchanged if every gated write inside a transaction were skipped. `grep -rn "\.Tx(" test/integration` finds no gated one either. Under [[D-020]] a verdict with no test that could fail is the same liability as a vacuous test, so this is 🟡 rather than ✅ — structural and correct, unpinned.
**If not ready:** one integration test: open a `Tx` on a gated repository, write a row into the caller's tenant, and assert the gate's own overwrite probe inside the same closure sees it. Worth writing down either way, because a consumer who assumes the probe reads the pool will conclude a row created earlier in the same transaction is invisible to the gate, and design around a problem that is not there.

### H-SECURITY-17 — Proving no repository escaped the gate
**Who:** the author of the twenty-model service, at review time
**Wants:** a failing test when somebody adds the twentieth repository and forgets the policy
**Story:** They add `Invoices.Bind(db)` at 6pm, it compiles, it serves, and every tenant reads every invoice.
**Must hold:**
1. A repository bound with no policy is visible somewhere other than in a diff.
2. Whatever makes it visible does not require naming all twenty by hand, or it is the same list that gets forgotten.
**Today:** ❌ missing
**Evidence:** `Bind`'s middleware list is variadic (`crud/sqlrepo/blueprint.go:246`), so a policy-free bind compiles and is silent. The package protects *itself* against a thirteenth verb with a totality test (`obligation_test.go:104`) and offers a consumer nothing equivalent one level up. Nothing in the tree walks a set of repositories and asks whether each is gated; `crud.SourceOf` and the `Next()` chain ([[D-061]]) make the walk possible — a `security.IsGated(repo)` over the same chain walk is a dozen lines — but it does not exist.
**If not ready:** they remember, or they write a start-up assertion over their own registry. In a twenty-model service the realistic leak is not a missing `WHERE`; it is one repository nobody wrapped, and the file that would catch it is the one nobody wrote.

### H-SECURITY-18 — Every relation a caller can reach is either narrowed or unreachable
**Who:** the same author, six months later, after somebody added a relation to a model
**Wants:** the two lists — what a caller may preload, and what the policy narrows — to be one list
**Story:** A new `Attachments` relation lands on `Article`. Nobody edits the query config, because it is empty. Nobody edits the policy, because nobody thought about it. The next `?preload=attachments` reads every tenant's attachments.
**Must hold:**
1. Adding a relation to a model does not silently add an un-narrowed preload.
2. The check that the two lists agree can be made once, not per relation.
**Today:** ❌ missing
**Evidence:** the set a caller can reach is decided in another module and is unbounded by default — `query.Config.Preloadable`, "Empty means anything the model maps" (`crud/query/compile.go:70-76`). The set the policy narrows is one `ScopeRelationField` per path (`policies.go:126-136`). Nothing compares them: the policy resolves its paths at declaration and panics on one that does not exist (`policies.go:148-167`), which catches a typo and not an omission. [[D-007]] is explicit that a relation nobody declared is read whole, and [[UC-004]] guarantee 11 says the same — this case is not a challenge to that decision, it is a request for the omission to be visible.
**If not ready:** they hold two lists in their head, and H-SECURITY-06's own story — "they add one line per relation path" — is the shape this file's opening says a consumer cannot be right about twenty times. Closing it is a test helper, not a runtime rule: something that takes a `query.Config` and a `Policy` and fails on a relation the config admits and the policy does not narrow. A dozen lines against two things already reachable at declaration time.

### H-SECURITY-19 — "My orders", where the owner is a person and the column is a `bigint`
**Who:** the author of anything that is not B2B — a consumer app, a device fleet, a document store
**Wants:** rows narrowed to the signed-in user, from the token's subject, in one line
**Story:** They read the module reference, find the constructor named for exactly this, write `security.ScopeSubject[Order, int64]("UserID")` against `user_id bigint`, and ship. The process starts. The first request panics.
**Must hold:**
1. The owner-is-a-person shape is a one-liner, the same way the owner-is-a-tenant shape is.
2. It works against the column types a `users` table actually has — `bigint`, `uuid`, `text`.
3. What it refuses on a create is the same thing the tenant helper refuses.
**Today:** ❌ missing — (3) holds where (1) and (2) do, which is only for a text column.
**Evidence:** `subject` always answers a `string`, because `Principal.Subject()` is a `string` (`principal.go:191-200`, `auth/principal.go:26`). `reconcile` then requires that string to be `ConvertibleTo` the column's type (`policies.go:62-71`), and Go converts a string to neither an integer nor a `[16]byte`. So `ScopeSubject[Order, int64]("UserID")` compiles, starts and panics on the first request, exactly as H-SECURITY-15 describes — with a much larger blast radius, because an integer user id is the more common schema, not the rarer one. The only test uses a string column: `principal_test.go:215` declares `Owner string` and `TestScopeSubjectNarrowsToTheCallersOwnRows` (`:223`) asserts the argument is `"u-1"`. Nothing catches the ordinary case.
`ScopeSubject`, `ScopeRelationSubject`, `ReadOnly`, `Freeze`, `RequireRole`, `RequirePermission` and `RequireAnyPermission` appear nowhere in this file's round 1 — six of the fourteen constructors — and the opening paragraph names "a person" as one of four owners and then never returns to it.
**If not ready:** `ScopeField` with an extractor that reads `auth.Require(ctx)` and parses the subject, five lines, correct, and with the row check and the freeze still bundled. The one-liner named for this job is available only where the id is text. This is the same defect as H-SECURITY-15 and is listed with it in the blocker table, because splitting them would let an owner fix the UUID half and think the release was clear.

### H-SECURITY-20 — Select three rows in the list, press delete
**Who:** any author who exposed the default routes
**Wants:** the same answer for "one of these is not mine" that the single-row delete gives
**Story:** The list screen has checkboxes. The client posts three ids to the bulk endpoint. One of them belongs to another tenant.
**Must hold:**
1. The answer to "one of these ids is not allowed" is the same shape as it is for one id.
2. Whatever that answer is, the client can tell a partial success from a whole one.
**Today:** 🟡 — it is safe and it is two different contracts on one endpoint, neither written down.
**Evidence:** `POST /{prefix}/bulk-delete` is registered by default whenever the handler is not read-only (`crud/http/crudnet/handler.go:165`) and maps straight through to `repo.Delete(ids...)` (`port/service.go:230-235`). Under a gate with a scope and no `Inspect`, that becomes one `DELETE … WHERE scope AND id IN (…)` (`security.go:695-713`): the foreign id is silently skipped and the response is `200 {"deleted": 2}` for three ids (`crud/http/crudnet/handler.go:403`). Under a gate with an `Inspect`, a *visible* row the rule refuses aborts the whole call with 403 and deletes nothing (`security.go:703-712`). One endpoint, two contracts, and the consumer's "select three, delete" UI reports success for a row that is still there.
The single-row `DELETE /{id}` on a foreign id answers 404 (`TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden`, `edge_test.go:157`), so this is also H-SECURITY-01 (2)'s second exception, and unlike the `PUT` one it is not recorded anywhere as a decision.
**If not ready:** nothing to write by hand — silently skipping is defensible and arguably the only thing consistent with [[D-008]], since telling the client which id was skipped is telling them it exists. What is missing is that the response says so: `{"deleted": 2}` against three requested ids is the whole signal, and no doc tells a consumer to compare the two numbers. One sentence in the module reference and one in the HTTP flow.

### H-SECURITY-21 — Soft delete under a gate
**Who:** the author who declared `sqlrepo.SoftDelete` because deletes have to be recoverable, and a tenant policy because rows belong to customers
**Wants:** the two declarations to compose
**Story:** A row is deleted and becomes a tombstone. Some time later a request creates a row at that id — a client-owned uuid, or an import that carries its own keys.
**Must hold:**
1. A write that lands on a tombstone belonging to another tenant is refused.
2. Two declarations that are each correct alone do not open something neither opens by itself.
**Today:** ❌ — this is the sharpest edge in the module and the file that grades the probe three times never reached it.
**Evidence:** `SoftDelete` ANDs `IsNull(deletedAt)` into the blueprint's scope (`crud/sqlrepo/blueprint.go:220`), and the blueprint scope is applied to every read including `Exists` (`crud/sqlrepo/repository.go:287-292`, `:587-590`). The gate's hidden-row probe is `g.Core.Exists(ctx, byID, crud.PrimaryOnly())` (`security.go:541`) — it goes *through* the repository, so it goes through the soft-delete filter. A tombstone answers `false`. `saveTarget` returns `(nil, nil)` (`security.go:548`), the action stays `Create`, and `Inspect` is then handed the **incoming** row, whose tenant is the caller's own and therefore passes. The `INSERT … ON CONFLICT` that follows lands on the tombstone and re-tenants it, with `err == nil`. `docs/ai/usecases/Index.md` gap 17 already records it. Nothing in `crud/decorators/security` tests soft delete against `Save`.
**If not ready:** there is no consumer workaround inside the policy — the probe is the gate's, and the policy cannot see it. Today they avoid the combination, or they never accept a client-owned key on a soft-deleting model. Closing it is one option on the probe read: `Exists` has to see tombstones for this one question, which means either a `crud` option that suspends the blueprint scope for a single read or a dedicated `crud.Core` method for "does this key exist at all". Whichever it is, [[D-031]] is the decision to name — declaring a soft delete declares both halves, and the gate has to be told that its existence question is about the key, not about the visible row.

### H-SECURITY-22 — "That email is already taken"
**Who:** the author of the signup form, on the same tenanted table
**Wants:** a 409 that says which field collided
**Story:** They wire the fault subsystem so a unique-constraint violation becomes a readable conflict instead of a 500. It works. Some months later somebody notices the form confirms which addresses exist in tenants the caller has never seen.
**Must hold:**
1. A conflict tells the caller about their own rows and no others.
2. Turning on two documented features does not reopen the isolation one of them was bought for.
**Today:** ❌ — the module ships half the remedy in its own vocabulary and nothing tells a consumer to write it.
**Evidence:** `probe.WithScope(fn func(context.Context) (crud.Predicate, error))` takes exactly the shape `Policy.Scope` already has, and its doc comment names [[D-008]] as the reason (`crud/probe/options.go:58-70`). Nothing correlates the two: `probe.Full(cat)` is declared against a `crud.Meta` and never sees the policy, the gate is a decorator that does not know a probe exists, and a probe wired without `WithScope` under a gate confirms rows across the boundary. Both usage guides pass it (`docs/usage-guides/gorm.md:1315`, `ent.md:1396`) and nothing fails if you do not. This is [[UC-004]]'s Gap 3, recorded against this module at `UC-004-isolate-tenants.md:138-142` — "Nothing in the gate addresses it and no test covers it" — and it is graded in full in `docs/ai/usecases/general/General.md` H-GENERAL-06 and `docs/ai/usecases/modules/faults/Faults.md` H-FAULTS-03.
Security's own half, which those two do not cover: `WithScope` is a **second declaration of the same rule**, in a second file, with nothing at declaration saying it is missing — which is H-SECURITY-17's complaint one level down, and the exact thing this module's central claim ("say it once") exists to prevent. And it is *only* the read half: [[D-042]] is explicit that a unique constraint a public endpoint can trigger is an oracle by construction and that the complete fix is not to have such an endpoint.
**If not ready:** they pass `probe.WithScope(policy.Scope)` per resource and keep it in step with the policy by hand. The correlation belongs at `Bind`, where the chain already holds both the gate and the probe; neither `security` nor `faults` can close it alone, which is why the blocker sits on General's row and this case exists to say the module's own vocabulary is half of it.

### H-SECURITY-23 — The consultant who belongs to three organisations
**Who:** the author of any B2B product on its second enterprise customer
**Wants:** `/orgs/{orgId}/articles` narrowed to that org, if the token says the caller is a member of it
**Story:** The token carries `"orgs": [1, 2, 3]`. The URL names one. The rule is "this org, and only if it is in that list".
**Must hold:**
1. A claim that is a list narrows to the whole list where the request names none.
2. Where the request names one — a path segment, a header, a subdomain — the policy can read it.
3. Whatever narrowing that produces reaches the `UPDATE` and the `DELETE`, not only the reads.
**Today:** ❌ missing for all three
**Evidence:** (1) is blocked by `ScopeField` compiling `crud.Eq` and nothing else (`policies.go:84`) — a list claim cannot become `IN (…)`. It is the third instance of that one defect and the one where the caller is legitimately authorised for several values.
(2) has no seam into the policy. `Policy.Scope` takes a bare `context.Context`, and nothing in the library puts a request value into a context except the auth middleware: `grep -rn "context.WithValue" crud auth port --include="*.go"` outside tests returns the executor binding, the principal, the request body, the locale and the logger, and no transport writes a path segment or a header.
(3) is the trap, and the library is honest about it in the one place it comes up. `crudnet.WithScope(func(*http.Request) ([]crud.Option, error))` exists on all four bindings and looks like the answer — it is not, and its own doc comment says so: "Reads only, and that is not a gap waiting to be filled: `Save` and `Delete` take no options… with a scope of `TenantID = 7`, `GET /{id}` on somebody else's row is 404 while `DELETE /{id}` on the same row answers 200" (`crud/http/crudnet/options.go:87-97`). It points the reader at `security.Gate` — which is where the seam runs out.
**If not ready:** the consumer's own middleware puts the org id on the context and their hand-written `Scope` reads it back — which works, is about fifteen lines including the context key, and gives up the helper's `Inspect` and `Immutable` again. It is the most common tenanted URL shape in the ecosystem and there is no documented answer anywhere in the tree.

### H-SECURITY-24 — "The projects I am a member of"
**Who:** the author of anything collaborative — shared documents, team boards, project tools
**Wants:** rows reached through a membership table narrowed the same way a column would be
**Story:** `project_members` joins users to projects. A user sees the projects they are on, and nothing else.
**Must hold:**
1. The narrowing is one statement, not one query per row.
2. A caller's own filter on the same relation cannot widen it.
3. Whatever expresses it keeps the create check and the frozen column, or says plainly that it does not.
**Today:** 🟡 — (1) and (2) are true and undocumented; (3) is the same loss as H-SECURITY-13 and H-SECURITY-14.
**Evidence:** the good news nobody has written down. `crud`'s predicate writer renders every relation hop as a correlated `EXISTS`, including the many-to-many join table (`crud/predicate.go:84-124`), and `correlate` falls back to the qualified table name when the outer statement has no alias (`crud/predicate.go:37-44`), so the same predicate compiles inside an `UPDATE`'s and a `DELETE`'s `WHERE` as well as a `SELECT`'s. So `Scope: func(ctx) { return crud.Eq("Members.UserID", uid), nil }` is a correct, single-statement, write-reaching membership scope **today**, and it is what H-SECURITY-07's "a per-row rule that has to ask the database" should be replaced by. (2) follows from `crud.Where` ANDing ([[D-004]]) and each hop opening its own subquery — a caller's `Members.UserID = someone-else` is a second `EXISTS`, not a wider one.
What is missing is that nothing says so, no helper produces it, and no test in this package covers a relation-crossing predicate in `Scope` — `grep -n "EXISTS" crud/decorators/security/*_test.go` returns nothing, so the write half is correct by reading only. (3) is the familiar loss: a hand-written `Scope` has no `Inspect`, so a create naming another project is unconstrained.
**If not ready:** the ten lines are already correct; write them down. This is the strongest argument for the `func(value any) crud.Predicate` seam of H-SECURITY-14 — it upgrades that proposal from "covers three read halves" to "covers four, one of which is the shape people leave this framework for" — and it needs its own must-hold in [[UC-004]]: a caller's nested filter on the same relation cannot widen the scope's `EXISTS`.

### H-SECURITY-25 — An authorisation decision, and a replica that has not caught up
**Who:** the author who added a read replica after the gate was already working
**Wants:** to know whether a 403 or a 404 was decided against stale data
**Story:** They split reads and writes. They wonder whether the row the per-row rule was shown is the row that is about to be written.
**Must hold:**
1. Every read the gate makes to decide a write goes to the primary.
2. A display read is still allowed to use the replica.
**Today:** ✅ ready
**Evidence:** every check read carries `crud.PrimaryOnly()` — `saveTarget`'s lookup and its hidden-row `Exists` (`security.go:525,541`), `Update`'s load-before-diff (`:613`), `UpdateAll`'s target fetch (`:664`), `Delete`'s and `DeleteAll`'s victim fetches (`:703,728`) — and the option's own doc names the reason ([[D-032]], `crud/options.go:167-173`). `GetByID` deliberately does not, because a display read's answer decides nothing (`security.go:262`). It is pinned end-to-end against two real databases that disagree: `TestTheGatesAuthorisationLoadTakesThePrimary` (`test/integration/replica_test.go:138`) inserts a different row on each side, asserts `Inspect` was shown the primary's copy, and carries the control that the same call succeeds once the primary agrees.
**If not ready:** n/a. Worth stating in the module reference, because by this file's own criterion — it has to hold in the places a consumer will not think about later — an unwritten guarantee is one they design around for no reason.

### H-SECURITY-26 — Turning the gate on over a service that already has endpoints
**Who:** the author of the twenty-endpoint service in this file's opening, on the day they finally do it
**Wants:** to see what the policy would have refused, before it refuses it
**Story:** They write the policy, bind it everywhere, and cannot ship it, because the first release either works or 403s production and there is no third outcome.
**Must hold:**
1. A policy can be run for its verdicts without those verdicts being enforced.
2. Turning enforcement on afterwards is one edit, not a re-declaration.
**Today:** ❌ missing
**Evidence:** `Gate` returns a `crud.Middleware` whose every refusal is a returned error (`security.go:183-195` and every verb below it); there is no observe mode, no counter, no hook that sees a denial. `Denied` builds an error and nothing consumes it (H-SECURITY-12). Grepping the package for `dry`, `audit`, `observe` or `report` returns nothing.
**If not ready:** they bind twice — the enforcing repository behind a feature flag, the ungated one in front of it — and get no verdicts at all, only the two behaviours. Or they read every handler. This is distinct from H-SECURITY-17: that one is the review-time question ("did anything escape"), this is the deploy-time one ("what breaks when I stop it escaping"), and it is the reason the twenty-endpoint author postpones the change. Closing it is a `Policy`-level seam that reports a denial through `port.Logger(ctx)` ([[D-062]]) and, under one flag, returns nil instead — small, and it has to be spelled so that the flag is impossible to leave on by accident.

### H-SECURITY-27 — Where the gate goes when there is more than one decorator
**Who:** any author on their second decorator — the specifications executor, the fault decorator, their own audit wrapper
**Wants:** to know which order is the right one and what the wrong one costs
**Story:** They have `Notes.Bind(pool, security.Gate(policy))` working. They add `faults.WithFaults(...)` to the same line and have to decide where it goes.
**Must hold:**
1. The order is stated somewhere a consumer reading `Bind` will find it.
2. What a decorator above the gate can and cannot see is stated, because that is the whole reason it matters.
**Today:** 🟡 — the mechanism is documented in one line, the consequence in none.
**Evidence:** `Bind`'s doc comment says "The first decorator ends up outermost" (`crud/sqlrepo/blueprint.go:244-245`) and `TestGateComposesWithOtherMiddleware` (`security_test.go:277`) pins it, asserting both the trace order across a gate and that the narrowing still reached the `WHERE`. The flagship example stacks two — `specs.Executor(Notes.Bind(crudpgx.Open(pool), security.Gate(policy)))` (`_examples/auth-jwt-gin/main.go:148`) — and says nothing about why that order.
The consequence is in this file three times and stated as an ordering rule nowhere. A decorator above the gate sees the caller's options and not the policy's narrowing, which is exactly why `probe.WithScope` has to exist (H-SECURITY-22) and why the specifications executor's own unbounded-write guard sees a predicate the gate has not yet ANDed its scope into (H-SECURITY-08).
**If not ready:** nothing to write by hand. What is missing is a paragraph, in this module's reference and in [[FL-013]], that says which side of the gate each shipped decorator belongs on and what it costs to get it wrong.

## The DX this should have

### The call site

```go
// Everything a tenanted resource needs, in the two lines the module doc promises.
policy := security.ScopeAttr[Article, int64]("TenantID", "tenant")
articles := Articles.Bind(db, security.Gate(policy))
```

**The ideal call site is byte-for-byte today's.** Nothing about the shape
changes. What changes is what those two lines do: today they narrow every read,
freeze the column and refuse a create into another tenant, and they also refuse
every create whose body omits the column and every replace that omits it. The
missing piece is a stamp, and the argument below is about the stamp and about
what has to be true for it not to be a downgrade.

Written in today's spelling on purpose. The post-`ID` spelling of blocker 6 does
not shorten this line — it moves the annotation to `Gate`, which cannot infer
`ID` from a `Policy[M]` because `ID` appears only in its result, exactly as
`Freeze[M, ID]` already demonstrates. Two lines, two type arguments today; two
lines, three afterwards.

**The rule the stamp has to follow**, because it and the refusal in
H-SECURITY-02 (2) cannot both be true otherwise: the stamp fills an **absent**
value and never overwrites a present one, so a body naming tenant 9 still
reaches `Inspect` and is still refused. Distinguishing absent from zero is
[[D-002]]'s problem and `Principal.Attr`'s own doc comment already cites it
(`auth/principal.go:35-38`). On a schema where 0 is a real tenant — the schema
`attrOf` already documents as the reason a missing claim is a denial
(`principal.go:171-174`) — a zero-value stamp turns a refusal into a silent
re-tenanting. So either the stamping hook reads a three-state input, or the
generated `<Model>Input` carries no tenant field at all and the stamp is
documented as safe only on a DTO that cannot express one. The second is smaller
and is what `cmd/vv -adapter` already generates.

### Turning one knob

```go
policy := security.Combine(
    security.PerAction[Article, int64](map[security.Action]auth.Permission{
        security.Read:   "article:read",
        security.Create: "article:write",
        security.Update: "article:write",
        security.Delete: "article:delete",
    }),

    // Support sees every tenant. An addition, not a rewrite: the frozen column
    // survives it, and a reviewer finds every lift of the scope by grepping one
    // identifier. The predicate is over the context and not over a role name,
    // because a rule naming a role is one this package already argues against
    // (principal.go:69-72).
    security.Except(
        security.ScopeAttr[Article, int64]("TenantID", "tenant"),
        // One field per enforcement hook Policy has. Zero lifts nothing.
        security.Lift{
            Scope:     true,
            Relations: true,
            Inspect:   true,  // see the note below — under ScopeAttr this is the scope
            Authorize: false,
            Immutable: false, // the console must still not re-file a row
        },
        func(ctx context.Context) bool {
            p, ok := auth.PrincipalFrom(ctx)
            return ok && auth.InAny(p, "platform-admin")
        },
    ),

    security.ScopeRelationAttr[Article, int64](
        Article_.Comments.Path(), Comment_.TenantID.Name(), "tenant"),

    // Reads forward, and named for its polarity: true refuses.
    security.RefuseWhen[Article, int64](security.Writes,
        func(_ context.Context, _ auth.Principal, art *Article) bool {
            return art.Locked
        }),
)

articles := Articles.Bind(db, security.Gate(policy))
```

Four independent facts, four values, one `Combine`. Nothing here is a branch
inside a closure, which matters because a branch inside a closure is invisible to
the next reader and to `grep`.

**`Lift` is total over `Policy`'s five enforcement hooks and every field
defaults to false.** That is not tidiness. H-SECURITY-04's must-hold 3 is that a
reviewer can find every way the scope can be lifted by reading one place, and a
struct that answers three of five questions is one a reviewer will assume
answered all of them. It needs the protection `obligation_test.go` gives the verb
seam — a reflect-based test that fails the build when a field is added to
`Policy` and `Lift` has no row for it — or the combinator becomes exactly the
hole [[D-030]] exists to prevent, one level up from the verbs.

**`Inspect: true` in the admin lift, not `Writes`.** Round 1 kept the row check
for writes so an admin could not create into an arbitrary tenant, and that is
wrong under `ScopeAttr`: `ScopeAttr`'s `Inspect` *is* the tenant equality check
(`policies.go:86-103`), and it starts by calling `attrOf` (`principal.go:175-187`),
which denies outright when the caller carries no such claim. A platform-admin
token with no `tenant` claim — the normal shape — would read every tenant and be
refused on every write, with the reason dropped at the wire; one that carries a
claim would be confined to that single tenant on every write. Keeping it is
reimposing the scope the lift just removed. What protects the console is the
freeze, which is why `Immutable` stays false. A per-row rule you want kept
belongs in a *sibling* of the `Except`, which is where `RefuseWhen` sits above.

**`RefuseWhen` and `InspectOwner` would ship with opposite booleans**, and that
has to be decided rather than left to the reader. `InspectOwner(allow func(...) bool)`
means true-permits; the same closure body under a refuse-shaped helper means the
opposite. A consumer who writes `InspectOwner(func(...) bool { return m.Locked })`
has inverted their rule silently, and the resulting policy allows exactly what it
meant to refuse and denies everything else — which no test of theirs would look
wrong. Either `RefuseWhen` replaces `InspectOwner` and `InspectOwner` becomes the
documented low-level form, or the package ships two per-row hooks in one import
whose booleans disagree and says so in both doc comments. `security.Writes` also
needs a set type: `Action` is a `uint8` with `iota` values (`security.go:29-37`),
not bit flags, so "neither edited nor deleted" is two calls unless a set exists.

**And it has to survive being written twenty times.** These constructors are
generic over `M any, ID comparable` with no further constraint, so an author can
write their own `func tenanted[M any, ID comparable](field string) security.Policy[M, ID]`
over them and call it once per resource — that works today and nothing says so.
What does not generalise is the relation line: `Article_.Comments.Path()` is a
generated per-model identifier and cannot be reached from a generic helper, so
the twentieth resource's relation narrowing is still hand-written per path. That
is the right place for the line to fall, and it is the line H-SECURITY-18 asks to
be made visible.

### Why this shape

**The declaration is the whole rule.** The failure this package exists to
prevent is a rule that is true in four places and forgotten in the fifth. Every
time part of the rule has to live somewhere else — a mapper, a service method, a
handler, a probe option — the count of places goes back up. Stamping on create is
the clearest case: the value being written is the value the narrowing already
computed, and asking the author to fetch it a second time from a different API is
asking them to get it wrong once.

The repository ships two counter-examples and both are worth naming rather than
arguing around. `probe.WithScope(policy.Scope)` makes the consumer hand the same
scope to a second declaration, or a signup collision confirms a row in a tenant
the caller has never seen (H-SECURITY-22). And the mapper that stamps the tenant
on create is a second copy of the same rule that does not run for a write made
from Go (H-SECURITY-02). A file whose central argument is "one place" has to
account for the two places the code says two.

**A knob is an addition, not a fork.** The current helpers are excellent right up
to the first "unless". After that the author drops to a hand-written `Policy`,
and the hand-written `Policy` is the exact shape [[UC-004]] Gap 1 documents as
dangerous — because the helper's `Inspect` and `Immutable` do not come along.
Four separate scenarios in this file land there (H-SECURITY-13, -14, -23, -24),
which says the problem is not four gaps but one: `ScopeField` compiles a single
predicate shape and offers no seam to widen it.

**Two different combinators, not one.** Round 1 merged the admin lift and the
anonymous reader and was wrong to. `Except` subtracts hooks; the anonymous reader
needs a *substituted* scope — published only — which no lift produces, and an
`Except` over `ScopeAttr` for an anonymous caller yields one who reads every
tenant. The lift is the smaller of the two, and it is also the one a second
`Bind` already serves (H-SECURITY-04). The substitution is the one nothing
serves, and it is what H-SECURITY-11 and H-SECURITY-23 both need.

**The alternative costs a leak, not just keystrokes.** A policy DSL with
conditionals inside it would be worse — [[D-021]] concentrates magic and this is
not one of the places. Combinators over values are ordinary Go and stay
greppable.

### What it must not break

- **[[D-007]] — narrowing crosses a relation only when declared.** Nothing above
  infers a far-side column. `ScopeRelationAttr` stays a separate line, because
  that the other model spells its tenant column the same way is a fact only the
  author knows. H-SECURITY-18 asks for the omission to be *visible*, not
  inferred.
- **[[D-008]] — out of scope is 404, not 403.** A stamping create must not
  become a way to learn an id exists; a stamp that fills an absent value changes
  no refusal, which is the whole reason it may only fill an absent one. The same
  decision is why H-SECURITY-20's bulk delete may not name the id it skipped.
- **[[D-002]] — absent, null and value are three states.** The stamp depends on
  it, and a stamp that cannot tell absent from zero is unsafe on the one schema
  `attrOf` already documents.
- **[[D-004]] — `Where` ANDs and never subtracts.** A lift may not be spelled as
  a predicate that widens; it has to remove a hook before the predicate is built,
  which is why `Except` wraps a `Policy` rather than contributing to a `Combine`.
- **[[D-026]] — the victim fetch carries the caller's options, and it is open.**
  H-SECURITY-07's `Limit` edge is this decision, not a fresh discovery. Anything
  proposed here picks one of its three options rather than routing around them,
  and it forbids making `DeleteAll` honour a `Limit` to make the two agree.
- **[[D-030]] — a new verb on the seam is a decorator obligation.** `Except`
  wraps a `Policy`, which has no verbs, so `obligation_test.go` keeps its grip —
  but `Lift` has fields per hook and needs its own totality test for the same
  reason.
- **[[D-031]] — a soft delete is one declaration, both halves.** H-SECURITY-21's
  fix has to be spelled so the gate's existence question is about the key rather
  than about the visible row, without giving a consumer a way to read tombstones
  by accident.
- **[[D-032]] — a replica never decides a write.** Every check read already
  carries `PrimaryOnly`; a new hook that reads before a write inherits that.
- **[[D-042]] — the probe is advisory.** H-SECURITY-22's remedy narrows what the
  probe asks about and never suppresses the driver's own violation.
- **[[D-055]] — a principal is a value in the context.** `Except` takes a
  `func(context.Context) bool` and not a `func(auth.Principal) bool` precisely
  because the anonymous case has no principal to hand it.
- **[[D-059]] — the HTTP projection of the error contract belongs to port.**
  H-SECURITY-12's remedy is not built here.
- **[[D-021]] — magic is preferred to Go orthodoxy, and it must fail at build or
  start-up rather than at request time.** This is the decision H-SECURITY-15 and
  H-SECURITY-19 breach: names are resolved at declaration and value types are
  not. Anything added here resolves at declaration.
- **A challenge, named:** a stamping hook puts a *mutation* in a package whose
  `Policy` doc calls its per-row hook a check, and it cannot be a fourth closure
  in `Policy` — `Save` runs `checkImmutableSave` before the incoming-row
  `Inspect` (`security.go:485-490`), so a stamp installed as an `Inspect` is
  refused by the freeze before it runs. It has to be a new hook the gate calls
  earlier. The mechanism is `Schema.Pointers` (`crud/access.go:18`) plus
  `reflect`, both already reachable from `policies.go`, so nothing about
  [[D-016]] is at stake. No decision forbids it and none permits it; one should
  be written, because `Inspect` takes a `*M` and a stamp smuggled in there today
  works by accident, which is the worse version of the same idea.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| Tenant isolation on reads, updates and deletes | 2 lines, 2 type arguments, exactly as advertised | none |
| Tenant isolation, per request | `ScopeAttr` installs an `Inspect`, so every `PATCH` pays an extra `SELECT` and every id-carrying `Save` a lookup plus, on a miss, an `Exists`; the scope-only policy is free and is not the shape the helper produces | small, and undocumented |
| Per-verb permissions | 6 lines, the map | none |
| Narrowing across a fixed relation path | 1 line per path, resolved at declaration | none |
| Narrowing that follows a model — a self-relation at depth | hand-written `RelationScopes` with `crud.RelationScopes.ForModel` and a `reflect.TypeOf`; no constructor reaches it | small |
| Frozen columns | free with `ScopeField`; `security.Freeze`, 1 line, otherwise; a typo panics at bind — and the free one is what produces the `PUT` failure in blocker 2 | none |
| A per-row veto | 4 lines with `InspectOwner`, written as an inverted boolean — and it re-prices the whole repository: projections cancelled on inspected reads, a `SELECT` before every `PATCH`, the victim set materialised on every filtered write, and every `Aggregate` refused once `InspectReads` is on | small to write, large to run |
| A create landing in the caller's tenant | nothing on the policy: an input type plus a `port.Mapper` (~10 lines, one implementation for all four transports, generated by `-adapter`) — and it never runs for a write made from Go, and it is a second copy of the tenant rule | large |
| A replace that omits the tenant column | the same mapper, or the client echoes a value the policy forbids it to change; otherwise `field TenantID is immutable` | large |
| A tenant column one hop away | hand-write `Scope` with a dotted `crud.Eq`; the helper panics at declaration; `Inspect` and `Immutable` do not come along | large |
| "Mine or the shared rows" | hand-write `Scope` with `Or`; same loss | large |
| "The projects I am a member of" | hand-write `Scope` with `crud.Eq("Members.UserID", uid)` — correct, one statement, reaches the writes, documented nowhere; same loss of `Inspect` | small once written down |
| "Unless the caller is an admin" | bind the blueprint twice: `Combine(PerAction(…), Freeze[Article, int64]("TenantID"))`, 3 lines, and two repository values nothing in the type tells apart, decided again per resource | small |
| "Unless nobody is authenticated" / an org named in the URL | no combinator substitutes a scope, and no transport puts a request value where a policy can read it; hand-write `Scope`, ~15 lines with the context key, and every principal-driven helper in the `Combine` has to go with it | large |
| Rows owned by a person | `ScopeSubject` is the one-liner and works only where the owner column is text; against `bigint` or `uuid` it panics on the first request | large |
| A UUID tenant key | `ScopeField` with a parsing extractor, ~5 lines; `ScopeAttr` panics at first request | small |
| A numeric claim against a text column | nothing: it converts to a rune and narrows to garbage, silently, forever | large |
| A worker with no request behind it | `auth.WithPrincipal(ctx, auth.Claims{…})`, ~5 lines, shown in the auth reference and in neither the security reference nor any guide or example | small |
| Opting into an unscoped bulk write | `p := Combine(...); p.AllowUnscopedDeleteAll = true` — the obvious edit (setting it on a sub-policy) silently does nothing, and the working one is in no document | small |
| A filtered delete that is actually guarded | nothing: the guard tests the predicate for nil, and an empty `NotIn` is `1 = 1` | large |
| A dashboard under a policy with `InspectReads` | there is none: `Aggregate` refuses outright | large |
| Soft delete plus a tenant policy | no workaround inside the policy; avoid the combination, or never accept a client-owned key | large |
| A conflict that does not confirm another tenant's row | `probe.WithScope(policy.Scope)` per resource, kept in step by hand; nothing correlates it with the gate | large |
| Rolling the gate out over live endpoints | nothing: a policy either enforces or is not bound | large |
| Testing a policy with no database | `policy.Authorize(ctx, …)` directly, 3 lines; the statement-level setup is ~8 lines per case and is charged on crudtest's scorecard, not this one | none |
| Proving no repository escaped the gate | nothing | large |
| Type parameters that mean something | `[Model, ID]` at all 14 constructors; `Policy` uses `ID` in no field | see blocker 6 |

**Overall:** on the straight path the enforcement is asserted where it can be
checked — at the statement, with negative controls beside the positives, a
totality test that fails the build when a verb reaches the seam undecided, and an
integration test over two disagreeing databases for the replica question. That is
better than most of this repository. Against it: two blockers write across a
boundary the module exists to hold — an empty `NotIn` walks through the
unscoped-write guard, and a tombstone is invisible to the overwrite probe — and
12 of 27 cases are missing outright. The distance opens in one place and it opens
four times: `ScopeField` compiles `column = value` and nothing else, so the child
table, the shared row, the membership join and the org in the URL all end at
"hand-write the policy", which is the shape the project's own use case documents
as dangerous. Customising means abandoning the short path rather than extending
it. And the advertised one-liner is a one-liner for one schema shape: a text
column, a flat claim, and a client willing to send `tenant_id` on `POST` and echo
it on `PUT`.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | The unscoped-write guard tests the predicate for `nil`, and a tautology is not nil: an empty `NotIn` or an empty conjunction renders `1 = 1` and walks straight through | blocker | `DeleteAll(Where(NotIn("ID", empty)))` empties one tenant's table under `ScopeAttr` and the whole table under a `PerAction`- or `Freeze`-only policy. `Specs.md` writes the counter-example against `security.go:724` by name; two layers exist to stop it and the same input walks through both. No test |
| 2 | A soft-deleted row is invisible to the gate's overwrite probe, so a save on a tombstone's id is treated as a create, checked against the incoming row's own tenant, and re-tenants somebody else's row with `err == nil` | blocker | Soft delete plus a tenant policy is an ordinary combination and the leak runs through the ordinary create endpoint. Already recorded as index gap 17; nothing in the package tests the two together |
| 3 | "Multi-tenancy in one line" refuses every create whose body omits the tenant column, and every replace that omits it, with two different messages and neither reason on the wire | serious | The documented happy path gives working reads and a 403 on the first `POST` and a different 403 on the first `PUT`; the fix is a second declaration in another file that does not run for a Go-side write |
| 4 | `ScopeField` compiles `column = value` and nothing else: a column one hop away panics at declaration, a shared row cannot be expressed, a membership join has no helper, and a list-valued claim cannot become `IN` | serious | Four common ownership schemas are not served by the helper the module leads with, and the fallback loses the bundled row check and frozen column — the documented dangerous shape |
| 5 | The claim's Go type must match the column's and nothing checks it before traffic: `ScopeSubject` panics on the first request against any integer or UUID owner column, and an integer claim against a text column converts to a rune and narrows to nothing, silently | serious | Both are the ordinary schema, not the exotic one, and it breaches [[D-021]] in the package where it matters most. The silent direction has no panic, no error and no log |
| 6 | `Policy[M, ID]`'s `ID` parameter is used by no field and is spelled at every one of 14 constructors — and dropping it moves the annotation to `Gate`, whose doc comment sells the inference it would remove | serious | Now-or-never: after a tag neither spelling can change. The arithmetic is a wash — a policy built from k constructors costs 2k type arguments today and k+2 after, so the flagship one-liner gets *worse* — which is why the decision has to be taken rather than defaulted |
| 7 | Nothing makes a policy conditional on the request or on whether anyone is authenticated: no combinator substitutes a scope, and no transport puts a path segment or header where a policy can read it | serious | The anonymous-plus-authenticated API and the `/orgs/{orgId}/…` URL are both month-one shapes, and the code they force is [[UC-004]] Gap 1. The cheap alternative — bind twice — serves the admin console and not either of these, because the same route answers both callers |
| 8 | A caller-supplied `Limit` truncates the rows a filtered write inspects while the statement carries no limit; with no limit the whole victim set is materialised in Go | sharp edge | [[D-026]], `Status: open` — `Limit(1)` on a gated filtered delete inspects one victim and deletes every match, and the unbounded case is an OOM on a cleanup job. Only reachable from Go |
| 9 | `InspectReads` plus any `Inspect` makes every `Aggregate` return 403, and `Combine` ORs the flag | sharp edge | A consumer who turns on row-level read checks loses every dashboard on that repository, with a reason the wire drops; no test covers the refusal |
| 10 | `Combine` ANDs `AllowUnscopedDeleteAll` / `AllowUnscopedUpdateAll`, so setting one on a sub-policy silently does nothing | sharp edge | The obvious edit fails closed, the working one is in no document, and the composed case has no test |
| 11 | Bulk delete answers a different contract from single delete for "one of these ids is not allowed": silently skipped and 200, against 404 for one id — and 403-and-nothing-deleted once an `Inspect` is set | sharp edge | Registered by default, reached by every list UI with checkboxes, and stated nowhere. `{"deleted": 2}` for three ids is the whole signal a client gets |
| 12 | Nothing detects a repository bound with no policy, and nothing reconciles the relations a caller may preload against the relations the policy narrows | sharp edge | In a twenty-model service the realistic leak is one ungated `Bind` or one relation added six months later, and both are invisible until a pen test |
| 13 | There is no way to run a policy without enforcing it | sharp edge | The first release of a gate over live endpoints either works or 403s production, which is why the change gets postponed and the leak stays open |
| 14 | Stale docs: `[[FL-007]]` and `[[FL-008]]` carry 36 `security.go:NNN` citations and 4 into `policies.go`, essentially all stale by 40 to 180 lines — `gate.Save` cited at `:337` is at `:460`, `saveTarget` at `:392` is at `:515`, `gate.DeleteAll` at `:569` is at `:716`. [[UC-004]] and `docs/ai/usecases/Index.md` still list the page total and the unvalidated frozen name as open when both are closed and pinned | sharp edge | A flow is the only place file paths and symbols appear, and it is what the next agent trusts instead of re-deriving. Half the citations misdirect and two index entries understate the code |

## Contested

- **Blocker 2's severity, and its promotion.** Round 1 rated the create-stamping
  gap `serious` and the DX lens argued the `port.Mapper` workaround is better
  than round 1 said. It is, and the file says so. It stays `serious` rather than
  dropping, because the workaround puts the tenant rule in a second file, does
  not run for a write made from Go, and is silent when absent — the failure this
  module exists to prevent, one layer up.
- **H-SECURITY-01 stays one case rather than splitting the `PUT` exception out.**
  It is 🟡 with the exception named and located, and not a blocker, because
  [[UC-004]] Gap 2 records it as a deliberate trade of confidentiality for
  integrity and the alternative is silently overwriting an invisible row. The
  bulk-delete exception is a separate case (H-SECURITY-20) because it is *not* a
  recorded decision.
- **H-SECURITY-04 dropped from `serious` to a mitigated 🟡 against round 1.** The
  DX lens is right that `security.Freeze` is the freeze without the scope — one
  exported line, in the module reference's own table, with a test that binds a
  gate carrying only a freeze. That was the objection propping up the "bind twice
  does not work" argument, and it was false. Binding twice works for the admin
  console. What remains a serious blocker is the *substituting* combinator, which
  binding twice cannot serve, and it is now blocker 7 with its own two cases.
- **Blocker 6 changed position rather than being kept.** Round 1 called the `ID`
  parameter noise to be deleted. The DX reviewer's arithmetic is right and is now
  in the call-site section: for the one-constructor policy the proposed fix is
  worse, and the honest post-`ID` flagship is 2 lines and 3 type arguments. The
  blocker survives because the decision is permanent after a tag, not because the
  current spelling is wrong.
- **The DX lens said the request half "has no seam at all"; kept as a correction
  rather than adopted.** `WithScope` exists on all four bindings
  (`crud/http/crudnet/options.go:87-97` and its three siblings). It is reads-only
  and its own doc comment states the asymmetry it creates and points at
  `security.Gate`. The conclusion survives — nothing reaches the policy — but a
  consumer will find that function first, so H-SECURITY-23 names it and says why
  it is not the answer.
- **H-SECURITY-08 and H-SECURITY-09 stay two cases.** A reviewer asked whether
  they are one case with two payloads. They fail differently — one is a guard
  that does not guard, one is a per-row cost — and the shared finding (a worker
  needs a principal in its own context) is now stated once, in H-SECURITY-08,
  and deferred to `General.md` H-GENERAL-13 rather than restated.
- **`Policy` has eight fields, not ten.** One reviewer's argument for a total
  `Lift` counted ten; the count is five enforcement hooks plus `InspectReads` and
  the two `AllowUnscoped` flags. The argument is right and is adopted; the number
  is corrected, because `Lift`'s totality test has to know what it is total over.
- **`ExceptRole` stays withdrawn in favour of `Except` over a context
  predicate.** The package's own doc comment argues against rules that name a
  role (`principal.go:69-72`), and a role predicate cannot serve the anonymous
  caller. The sugar can be a two-line wrapper if anyone wants it.
- **H-SECURITY-12's case is kept and its blocker row is dropped.** The reason
  surviving in the Go error and dying at the wire is security's own half and
  worth grading; the remedy is `porthttp.Renderer`'s and is counted on
  `Port.md`'s row. The same is done for H-SECURITY-22, whose blocker is counted
  on `General.md`'s row 6.

## Edge cases

### E-SECURITY-01 — A gate attached with no policy
**Shape:** misuse
**Setup:** A role-derived policy list becomes empty during boot, but the resource still binds through `security.Gate`.
**What the consumer does:** They read `Docs.Bind(db, security.Gate(security.Policy[Doc, int64]{}))` as a guarded repository and expose its ordinary list route.
**What must happen:** Binding must refuse, or unrestricted access must require an explicit declaration whose name says so. A gate left with no rule must not make every row readable.
**Today:** ❌ wrong or unhandled
**Evidence:** `Gate` only stores the policy and builds the immutable-name index (`security.go:126-129`); `scope` returns a nil predicate when no scope hook exists (`security.go:197-201`), and `scoped` then returns the caller options unchanged (`security.go:225-237`) to `GetAll` (`security.go:323-338`). `TestCombineOfNothingIsNoMorePermissiveThanTheZeroPolicy` (`gate_edge_test.go:184-206`) pins only the unscoped `DeleteAll` refusal. No test sends a read through a zero policy.
**Blast radius:** data leak

### E-SECURITY-02 — The tenant resolver answers no value and no error
**Shape:** adversarial input
**Setup:** A hand-written `Scope` looks up the workspace from a host name and its missed-map branch returns `(nil, nil)`.
**What the consumer does:** They send a request for an unrecognised host and expect the gate to fail closed, as it does when the resolver returns an error.
**What must happen:** A scope that cannot name a tenant must return an error before a statement runs. Unrestricted access needs an explicit, auditable declaration rather than the same nil value a failed lookup produces.
**Today:** ❌ wrong or unhandled
**Evidence:** `Policy.Scope` documents a nil predicate as unrestricted (`security.go:55-60`), and `scoped` omits a nil predicate and delegates the original options (`security.go:225-237`). By contrast, `TestAScopeThatFailsClosesEveryDoor` (`edge_test.go:47-121`) covers only the error result and asserts zero statements; no test covers `(nil, nil)`.
**Blast radius:** data leak

### E-SECURITY-03 — A list UI submits no selected ids
**Shape:** boundary
**Setup:** A user clears their selection while the bulk-delete request is being built.
**What the consumer does:** Their service calls `repo.Delete(ctx)` with no ids.
**What must happen:** It must report zero deletions and issue neither a read nor a write; an empty list must not be reinterpreted as an unscoped delete.
**Today:** 🟡 partial
**Evidence:** `Delete` returns `(0, nil)` before authorisation or a repository call when `len(ids) == 0` (`security.go:677-680`). No empty-id case exists in `crud/decorators/security/*_test.go`.
**Blast radius:** none

### E-SECURITY-04 — One mapper result in a batch is nil
**Shape:** boundary
**Setup:** A batch importer maps a malformed record to nil after it has mapped earlier records successfully.
**What the consumer does:** They pass that slice to `SaveAll` under a tenant policy.
**What must happen:** The whole batch must be refused before the one write statement; a malformed later item must not commit the valid prefix.
**Today:** 🟡 partial
**Evidence:** `SaveAll` checks every item, rejects a nil model, and calls `g.Core.SaveAll` only after the loop completes (`security.go:412-457`). `TestSaveAllIsCheckedByTheGate` is named in [[D-030]], but no security-package test exercises a nil item after a valid one.
**Blast radius:** none

### E-SECURITY-05 — The far-side tenant column has a different Go type
**Shape:** degenerate declaration
**Setup:** An article has an integer tenant id, its preloaded comments have a text tenant id after a migration, and the token claim remains an integer.
**What the consumer does:** They combine `ScopeAttr` with `ScopeRelationAttr` and expect the one claim to fail closed consistently on both tables.
**What must happen:** The relation helper must validate or reject an incompatible value rather than leave its conversion to a driver at request time. A nil custom relation value must be refused for the same reason.
**Today:** ❌ wrong or unhandled
**Evidence:** Root `ScopeField` obtains the target type and reconciles or rejects its value (`policies.go:53-84`). `ScopeRelationField` resolves only the far-side field name and passes the raw value to `crud.Eq` (`policies.go:126-135`); `ScopeRelationAttr` and `ScopeRelationSubject` are thin wrappers over it (`principal.go:154-166`). The relation tests use matching `int64` and string fixtures (`principal_relscope_test.go:42-75`); no mismatch or nil-value test exists.
**Blast radius:** confusing error

### E-SECURITY-06 — Two independent tenant checks disagree
**Shape:** misuse
**Setup:** A service keeps the tenant in both the authenticated principal and a verified route context while it migrates endpoints.
**What the consumer does:** It combines the two scopes so disagreement returns no rows rather than picking whichever declaration happened to run last.
**What must happen:** Both predicates must remain in the statement, including when they name the same field with different values.
**Today:** 🟡 partial
**Evidence:** `Combine` collects each non-nil scope and returns `crud.And(ps...)` (`policies.go:198-237`); `TestPoliciesCombine` checks that two root predicates reach one `WHERE` (`security_test.go:256-273`). That test uses compatible predicates, not conflicting tenant values.
**Blast radius:** none

### E-SECURITY-07 — A configuration reload edits the declaration after bind
**Shape:** misuse
**Setup:** A program holds a `Policy` value in configuration, binds a repository, then edits its immutable-field slice or unscoped-write flag for the next revision.
**What the consumer does:** It expects the repository already published to keep the policy it was bound with.
**What must happen:** A bound gate must be a stable snapshot; later changes to the declaration must not silently change what an in-flight service permits.
**Today:** 🟡 partial
**Evidence:** `Gate` captures `p` by value and builds an immutable-name map at bind (`security.go:126-160`); update checks use that map (`security.go:645-654`) and the unscoped guards use the captured policy flags (`security.go:660-661`, `724-725`). `PerAction` separately copies its input map (`principal.go:103-125`), but no test mutates a general `Policy` after binding.
**Blast radius:** confusing error

### E-SECURITY-08 — Two tenants use one bound repository concurrently
**Shape:** concurrency
**Setup:** Tenant A and tenant B send list and update requests through the same repository at the same time.
**What the consumer does:** Each request supplies its own principal in its context and expects every predicate argument to stay with that request.
**What must happen:** No root scope, inspection value or relation option from one request may appear in another request's statement.
**Today:** ❓ unverified
**Evidence:** The gate derives `p`, `rel` and `scoped` in call-local variables (`security.go:225-237`), and `ScopeField` derives its value in a closure-local variable (`policies.go:74-102`). There is no `t.Parallel`, goroutine or concurrent-use test in this package. The result also depends on the consumer's extractor, which the gate cannot make safe for them.
**Blast radius:** data leak

### E-SECURITY-09 — Concurrent preloads keep their relation narrowings separate
**Shape:** concurrency
**Setup:** Two tenant requests preload the same relation through one policy at once.
**What the consumer does:** They expect tenant A's relation predicate not to persist into tenant B's preload, or vice versa.
**What must happen:** Each call must build a fresh relation narrowing and treat an error as a refusal; no mutable relation-scope value may survive to a later request.
**Today:** ❓ unverified
**Evidence:** `ScopeRelationField` constructs an `AtPath` value inside its per-context closure (`policies.go:126-135`), and `gate.narrow` invokes the policy for each operation (`security.go:207-218`). `TestARelationScopeErrorFailsClosed` and the merge tests cover error and composition paths, but there is no concurrent relation-scope test in `crud/decorators/security`.
**Blast radius:** data leak

### E-SECURITY-10 — The request is cancelled after the inspection read
**Shape:** partial failure
**Setup:** A filtered write has loaded its victims and the caller disconnects while a later `Inspect` callback is running.
**What the consumer does:** Their callback returns nil because it has no work left, while the request context is already cancelled.
**What must happen:** The gate must not send an `UPDATE` or `DELETE` after cancellation; cancellation is not consent to finish a write the caller abandoned.
**Today:** 🟡 partial
**Evidence:** `UpdateAll` and `DeleteAll` pass the context to the victim read and stop on an `Inspect` error, but neither checks `ctx.Err()` between the inspection loop and the final write (`security.go:663-674`, `727-738`). No cancellation case exists in the package tests. Whether the final source declines the write is therefore outside the gate's guarantee.
**Blast radius:** data loss

### E-SECURITY-11 — A caller may create but may not overwrite
**Shape:** partial failure
**Setup:** A client-owned id is allowed for creation, but the principal has `Create` permission and lacks `Update` permission.
**What the consumer does:** They call `Save` with an id that happens to name an existing row and expect the action check before source data is read.
**What must happen:** A permission refusal must not depend on reading the row first, or the module reference must state that `Save` needs an existence probe before it knows which action to authorise.
**Today:** 🟡 partial
**Evidence:** `Save` calls `saveTarget` before it calls `authorize(Update)` for an existing id (`security.go:471-501`); `saveTarget` issues `GetAll` and may issue `Exists` (`security.go:515-548`). The seam-totality test's `Save` row supplies a model with no id (`obligation_test.go:57-61`), so it does not pin this order. The write is refused, but the advertised before-any-SQL coarse check does not hold for this path.
**Blast radius:** confusing error

### E-SECURITY-12 — A selection contains tens of thousands of ids
**Shape:** scale
**Setup:** An administrator selects a large result set, or an integration worker retries a large id list, under `ScopeAttr`.
**What the consumer does:** They call `Delete(ctx, ids...)` and expect either bounded work or a clear refusal rather than memory growing with the selection.
**What must happen:** The gate must cap, batch or reject an inspected id set before it materialises every victim and emits one enormous `IN` predicate.
**Today:** ❌ wrong or unhandled
**Evidence:** `Delete` builds `crud.InAny(pk, ids)`, loads the complete victim slice when `Inspect` is set, walks it, and only then issues `DeleteAll` (`security.go:691-713`). `ScopeAttr` installs `Inspect` through `ScopeField` (`policies.go:74-105`). The package tests its all-or-nothing batch behaviour with two rows (`edge_test.go:276-336`), not a bounded or oversized id set.
**Blast radius:** crash

## Edge verdict

The worst new finding is a declaration that visibly contains `security.Gate` but
no effective policy: both a zero `Policy` and a hand-written scope returning
`(nil, nil)` delegate ordinary reads without a predicate. The gate is closed
against an explicit scope error and checks a batch before its final statement,
but the far-side helper does not validate values as the root helper does, and a
large selected-id delete still materialises every victim. The per-request state
looks local by reading, yet neither root nor relation narrowing has a concurrent
test, and cancellation after inspection is not a gate-level refusal.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `security.Gate(security.Policy{})` makes all ordinary reads unscoped | blocker | A configuration list becoming empty leaves a visibly gated repository that reads every tenant's rows; only the two bulk-table guards remain. |
| 2 | A hand-written `Scope` returning `(nil, nil)` is indistinguishable from an intentional admin bypass | blocker | One missed lookup branch turns an unknown tenant into an unfiltered read with no error or audit signal. |
| 3 | `ScopeRelationAttr` and `ScopeRelationSubject` pass raw claims to the far-side predicate without the root helper's type reconciliation | serious | A normal schema migration or nullable value moves the decision to driver coercion at request time, against the module's own eager-validation standard. |
| 4 | The gate does not check cancellation after its inspected-victim loop | sharp edge | A source that does not independently reject a cancelled context can receive a write after its caller has abandoned the request. |
| 5 | `Delete(ids...)` with `ScopeAttr` has no size bound and materialises every selected victim before deleting | sharp edge | A large selection can exhaust process memory or a driver parameter budget on a route consumers routinely expose. |
