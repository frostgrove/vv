# vv — a filtered, sorted, paginated, secured CRUD API over the model you already have

**Covers:** `github.com/frostgrove/vv` and every module under it — `crud`, `crud/sqlrepo`, `crud/query`, `crud/decorators/{specs,security,faults}`, `crud/adapter/{crudsql,crudpgx}`, `crud/{catalog,probe,sqlfault,crudtest}`, `crud/http/{crudhttp,crudnet,crudfiber,crudgin}`, `crud/rpc/crudgrpc`, `auth`, `auth/{apikey,authjwt}`, `auth/http/{authhttp,authnet,authgin,authfiber}`, `auth/rpc/authgrpc`, `port`, `port/porthttp`, `remote`, `remote/remotehttp`, `errs`, `errs/sqlerr`, `utils/{vvdb,vvflag,vvcfg}`, `utils/vvdb/dbpgx`, `cmd/vv`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — every part is deep and proven in isolation; a stock mount is writable by anyone who can reach the port, open in five query dimensions and serialises the whole model, the README's flagship error example promises a status the code does not answer, the wiring the README prescribes for the error vocabulary configures nothing on a generated route, and nothing assembles the parts into an application. The edge pass adds a cross-stack ambiguity: two HTTP credentials select a tenant by header order instead of being refused before the generated route. The `Save` and gRPC numeric-key integrity defects are tracked by their canonical Sqlrepo and Crudgrpc sweeps.

**This table is not the whole gate.** It carries what no single module owns. The
module sweeps carry their own active blocker-severity rows, and a tag has to
clear both sets. Closed findings remain in those tables as labelled historical
evidence, so a raw row count is no longer a readiness metric. The rollup is under
"Release blockers found here".

## What a consumer is actually trying to do

Somebody has a database with twenty tables and a deadline. Sixteen of those
tables need the same six endpoints — list it, filter it, page it, fetch one,
create, patch, delete — and the four interesting ones need something written by
hand. They are here to stop writing the sixteen. What they are buying is not a
feature; it is the promise that the twentieth resource costs what the first one
did.

They do not arrive empty-handed. There is already a pool, a driver, an ORM they
like parts of, migrations that work, and a transaction in the middle of a use
case that already does the right thing. The first question they ask is not "what
can it do" but "what does it take away". If adopting this means handing over the
connection, rewriting the models, or giving up the one query that carries the
business, they stop reading. The second question is what it costs per request —
in CPU and in round trips — because somebody in review will ask.

Before any of that there is the part every application has and nobody calls a
feature: the request has to be checked before the database sees it. Required,
max length, an email that looks like one, a date that is not in the past, a
status that is one of four. A form needs to be told all three things that are
wrong with it, not the first — and it does not care which of the three the
database found and which the server found.

The third thing they want is the boring middle. Every row belongs to a tenant
and no query may forget it. Some columns must never leave the building — the
password hash, the internal note, the salary — and "never" has to include being
filtered on and sorted by, not only being printed. A list that somebody is
scrolling must not shuffle under them. `updated_at` has to be right. A deleted
row has to be recoverable on Tuesday and gone for good on Friday. None of that
is interesting and all of it is where hand-written CRUD goes wrong, quietly, in
production.

By the second month the shape of the application matters more than any single
endpoint. There is one `main`, twenty resources, a worker fleet that writes to
the same tables with no HTTP request in sight, an import that lands fifty
thousand rows on a Tuesday, and a rule — *nobody reads another tenant's rows* —
that has to hold on every path including the ones nobody remembered to list.
What they need from the framework at that point is a place to say a thing once.

Then it goes wrong at 3am. A migration landed on half the fleet, a request
answers 500, and the only question that matters is why. Everything the API
correctly refuses to tell the client has to be somewhere the operator can read.

And they need to be able to leave. Their models stay their models, their
handlers stay ordinary handlers, and any endpoint that turns out to need real
code gets real code without the rest unravelling.

## Happy cases

### H-GENERAL-01 — The first hour: from nothing to a curl
**Who:** a backend developer evaluating on a Tuesday afternoon, with a Postgres container and one table
**Wants:** a working, filterable endpoint over an existing table before the afternoon is gone
**Story:** They add the dependency, paste their struct, add `db` tags, run the generator, declare a repository, mount it on the router they already use, and curl it. If it takes more than one page of code they close the tab.
**Must hold:**
1. Installing is one line for the stdlib path, two if they use a framework or a non-stdlib driver.
2. The model is the struct they already have.
3. A mistake in the declaration — a tag that names nothing, an id type that does not match the key — stops the process at start-up and names the field, rather than showing up as a wrong answer later.
4. A complete program they can copy compiles and serves against an empty database, and every file it needs is in the directory it is in.
**Today:** 🟡 partial — 1 and 4 hold; 2 has a second struct in it; 3 has a hole shaped exactly like an ORM model
**Evidence:** `_examples/sql-nethttp/main.go` is 121 lines, and beside it is a 1589-byte `vv_gen.go` the example does not mention — `sqlrepo.Define[Product, int64, ProductUpdate]` takes a third type parameter and `main.go:39` carries the `//go:generate` line that produces it. The README says so outright at `README.md:137` ("Declare the model, the update DTO and the repository"), so the second struct is disclosed. Guarantee 3 holds for the cases it covers: an unexported field carrying a `db` tag is a `SchemaError` naming the field (`crud/meta.go:361-368`). It does not hold for a struct-shaped one: `crud/meta.go:372-377` treats any struct-shaped field as a relation candidate and `continue`s it when there is no `rel` tag, **whether or not it carries an explicit `db:"…"`**. An embedded value object and a custom struct with no `Valuer` are both dropped in silence — the consumer asked for a column, got none, and reads a zero forever. **Round 1 named `json.RawMessage` here and that was wrong**: `relCandidate` unwraps a slice to its element, sees `uint8`, and returns `ok == false` (`crud/relation.go:186-196`), so it is mapped as an ordinary column. So is `map[string]any` — which is the other half of the same hole, and arguably the worse one: a JSONB field tagged `db:"metadata"` is mapped as a column, reaches the driver, and fails there at run time rather than at `Define`. Guarantee 3's second miss is the query configuration, which H-GENERAL-10 carries.
**If not ready:** they discover the dropped column by reading a zero. The fix is the shape already in `meta.go` one branch up — refuse a struct-shaped field that carries an explicit `db` tag and no `rel` tag, naming the field and the two ways out (`db:"-"`, or a `Valuer`). Filed independently as row 5 of the `crud` sweep, where it is `serious` rather than `blocker`.

### H-GENERAL-02 — Whose job is the table, the schema and the token
**Who:** the same developer, ten minutes earlier, deciding what they still have to build
**Wants:** to know where the framework's responsibility stops before writing anything
**Story:** They read the quick start, see `Define("users")`, and look for the part that creates the table. Their tables are in a schema called `app`. Their auth section shows a token being verified and they wonder who mints it.
**Must hold:**
1. It is stated, once and early, that the library never creates or alters a table.
2. A table that is not in the connection's default schema can be named, or the fact that it cannot is written down.
3. It is stated that verifying a credential and issuing one are different jobs, and the library does the first only.
**Today:** 🟡 partial — 1 is true in the code and never said in consumer prose; 2 is neither supported nor written down; 3 is said in one example's comment and nowhere else
**Evidence:** `[[D-057]]` owns the connection half and says nothing about DDL. **Round 1 said the examples never explain their `bootstrap` and that was wrong** — all nine carry the sentence in the doc comment above it (`_examples/sql-nethttp/main.go:98-99`, `_examples/pgx-fiber/main.go:106-111`, and `_examples/ent-pgx-fiber/main.go:111-114` goes further: "the point here is that vv takes no part in either step and needs none"). So guarantee 1 fails in the README's Quick start only, which is where an evaluator reads. Guarantee 2: `Quote` wraps the whole string as one identifier on every dialect (`crud/dialect.go:70-72`, `:114-116`), so `Define("app.users")` renders `"app.users"` and fails; `grep -n schema crud/sqlrepo/blueprint.go` is empty — there is no schema setting. The workaround is a `search_path` on the connection, which is a DSN concern nobody names and which interacts with pooling. Guarantee 3 exists as a comment on `printTokens` in one example (`_examples/auth-jwt-gin/main.go:167-169`: "A real service does not issue its own tokens; this is the identity provider the example does not have").
**If not ready:** they find out about the table by getting `relation "users" does not exist` from the driver, and about the schema by getting the same error from a table that exists. One paragraph in the quick start — migrations stay yours or your ORM's, tokens stay yours or your IdP's, and a non-default schema is a `search_path` and not a table name — closes all three, and it is the natural home for H-GENERAL-19's start-up check.

### H-GENERAL-03 — The same resource on any stack and any transport
**Who:** the tech lead choosing between Gin, Fiber, net/http and gRPC while the team argues
**Wants:** the choice of router and driver not to be a choice about the API
**Story:** They write one resource against `database/sql` on net/http, then move it to pgx on Fiber, then expose the same thing over gRPC, and expect the routes, the grammar and the statuses to be the same.
**Must hold:**
1. The same request gets the same status and the same body on every binding.
2. Changing the driver or the router does not change what the API answers.
3. A resource whose API shape differs from its model shape is still one mount, with a mapper.
**Today:** ✅ ready
**Evidence:** guarantee 1 is `test/portmount/mount_test.go:340`, which asserts three bindings hand the service byte-identical commands, and `test/portmount/grpcmount_test.go:112`, which extends it to the fourth transport; `[[UC-001]]` is the route set. Guarantee 2 is the nine runnable examples, which cover ent, gorm, sqlx, `database/sql` and pgx on Gin, Fiber, net/http and gRPC. **Round 1 used the examples as evidence for guarantee 1 and that was wrong**: the nine examples are nine independent re-declarations — `grep -n "^type [A-Z][a-zA-Z]* struct" _examples/*/main.go` shows eight separate `Product` types across seven files plus `Note`/`NoteUpdate` at `_examples/auth-jwt-gin/main.go:59,71` — so they prove nine programs compile, not that one declaration mounts unchanged. That is the same evidence error this file's own scope note says it fixed one guarantee earlier.
**If not ready:** n/a.

### H-GENERAL-04 — One `main` for twenty resources
**Who:** the tech lead who owns `cmd/api/main.go` and reviews every change to it
**Wants:** the security policy, the query bounds, the error wiring and the renderer declared once and applied to every resource
**Story:** They wire the stack for the first resource, like it, and then need the same stack on nineteen more. They want a value they can hand to each mount, not nineteen copies of six lines that drift.
**Must hold:**
1. The security policy, the page bounds, the error messages and the typed-query wrapper written for resource one apply to resource twenty without being retyped.
2. Only the genuinely per-model parts — the table name, the allow-lists — differ.
3. A resource that opts out of one part keeps the rest.
4. Leaving one out on resource fourteen is visible.
**Today:** ❌ missing
**Evidence:** nothing found for this. There is no composition package, and the one program in the tree that wires four resources is generated per resource and hands each `MountX` a finished service (`_examples/example/blog/vv_gen.go:144`, `:234`, `:333`, `:407`) — it absorbs the type parameters and nothing else. Everything else is per-resource: the decorator list at `Blueprint.Bind(src, mw...)`, the Criteria API at `specs.Executor(...)` (nine examples, nine call sites), the bounds at `crudgin.WithQuery[M, ID, U](cfg)`, and the renderer at `WithRenderer[M, ID, U]` whose default value is unexported (`crud/http/crudgin/options.go:143-146`). **Five things can be silently missing on resource fourteen** — the probe, the page cap, the allow-lists, `specs.Executor` and the renderer — and none of them fails when it is left out. Guarantee 2 is half true and the half is load-bearing: a shared `*query.Config` carries the numeric limits fine, but `Config.Check` resolves allow-list entries against one model's `crud.Meta` (`crud/query/compile.go:152`, `:203`), so a shared value carrying allow-lists is validated against whichever model checked it — if anything checked it at all, which H-GENERAL-10 shows nothing does. `[[D-037]]` reserves the name `app` and rules out a component graph; the package it constrains is unwritten.
**If not ready:** they copy six lines twenty times. Guarantees 1 to 4 are closable by one small package (the `vvkit` sketched below) for four of the five elements. The renderer is the exception: `[[D-033]]` keeps a package that can produce `[]crudgin.Option` out of the tree, so the catalogue stays per-mount until each binding grows a half of its own. **Round 1 also claimed `crudgin.Serving` panics on a renderer and that was wrong** — `RefuseServiceOptions` switches on `r.Query != nil` and `r.AllowClientID` and nothing else (`port/rules.go:91-98`), and `WithRenderer` is not a `Rules` field at all, so `Serving(svc, WithRenderer(r))` is legal today. D-033 alone is the constraint.

### H-GENERAL-05 — Refusing a bad payload before the database sees it
**Who:** every developer with a signup form, on the first endpoint that takes user input
**Wants:** required, max length, email format and a cross-field rule checked on the way in, in the same envelope as everything else that can go wrong
**Story:** They mount `POST /users`, add their validator, and post a body with a malformed email that is also already taken. They expect one response listing both.
**Must hold:**
1. There is a place to run a validator on a generated route.
2. That place is the same on every transport, and it is the same place a worker's writes go through.
3. A rule the application refused and a constraint the database refused arrive in one body, told apart rather than split across two round trips.
**Today:** ❌ missing for 2 and 3 — and 3 is the README's own stated point
**Evidence:** the only seam on a generated route is `BeforeSave`/`BeforeUpdate`, which take a transport-shaped argument in every binding (`crud/http/crudnet/options.go:102`, `:107` take `func(*http.Request, *M) error`; `crudfiber`, `crudgin` and `crudgrpc` each take their own), so guarantee 2 fails twice over — it is a different signature per transport and it is absent on the worker path, which is H-GENERAL-15. Guarantee 3 fails structurally: `port/service.go:158-161` runs `cmd.Before` and **returns on error before `s.repo.Save`**, so a refused payload never reaches the write that would have found the unique violation. The client gets the application's violations or the database's, never both. The bridge exists — `errs.FromFieldViolations` turns a validator's errors into `errs.Violation`s (`errs/bridge.go:46`) — and `grep -rn FromFieldViolations --include="*.go" . | grep -v _test` returns only its own definition and doc: **nothing in the mounted path calls it**. `README.md:1186-1189` says the opposite is the point: "A rule a validator refused and a constraint the database refused end up in the same list, of the same type… Merging them is the point: a payload with a malformed email *and* a taken email is two violations at one path."
**If not ready:** they write a service type (H-GENERAL-13) that validates, calls `Save`, catches the fault and merges the two lists by hand — about thirty lines per resource, and the merge is the part that is easy to get wrong. What would close guarantee 3 is a repository-level or service-level hook that collects rather than returns: run the rules, keep the violations, still attempt the write, and merge. That is a decision about the write path and not a helper, which is why it is here and not in `port`'s sweep.

### H-GENERAL-06 — The resource nobody put a gate on
**Who:** the developer who followed the quick start and shipped it behind a load balancer
**Wants:** the default mount not to be the dangerous one
**Story:** They mount `/users` the way the README shows, put the auth middleware in front of the router, and go home. Somebody with a valid token — any valid token — sends `DELETE /users/1`.
**Must hold:**
1. What a stock mount publishes is stated where the mount is shown.
2. A repository with no authorization attached does not silently publish destructive verbs.
3. The option a consumer reaches for when they want per-request narrowing does what its name suggests on every verb.
**Today:** ❌ missing — all three
**Evidence:** `Mount` registers `POST` on the collection, `POST /bulk-delete`, `PATCH /{id}`, `PUT /{id}` and `DELETE /{id}` unless `ReadOnly()` was passed (`crud/http/crudnet/handler.go:149-179`), and `ReadOnly` is opt-in. `security.Gate` is per-`Bind` and nothing at mount time notices that the repository underneath carries no policy — the quick start's own shape, `crudnet.New(repo).Mount(mux, "/users")`, is world-writable to anyone the auth middleware lets through, and the README never says so. Guarantee 3 is a cross-module naming collision that no single module owns: `security.ScopeAttr` is protection, and `crudnet.WithScope`/`crudgin.WithScope`/`crudfiber.WithScope`/`crudgrpc.WithScope` is a **read** filter with the same word in it. Its own doc comment is exact about the consequence — "it looks like protection and is not — with a scope of TenantID = 7, GET /{id} on somebody else's row is 404 while DELETE /{id} on the same row answers 200" (`crud/http/crudnet/options.go:88-97`) — and `README.md:960` lists `WithScope` in a bare comma-separated line beside `WithTransform` and `BeforeSave`, with no warning at all. A consumer who reaches for the name they saw in that list gets a resource whose reads are scoped and whose deletes are not.
**If not ready:** they read the option's doc comment, or they do not. Guarantee 1 is a sentence in the quick start. Guarantee 2 is the strongest argument in this file for making the gate **opt-out**: a mount over a repository with no policy should have to say so. Guarantee 3 is a rename or a doc line at the one place a consumer meets the name, which is the README's option list.

### H-GENERAL-07 — Tenant isolation that holds on every path
**Who:** the author of a multi-tenant SaaS, the week before the first customer with a lawyer
**Wants:** one declaration per resource after which no request, on any route, can read another tenant's row
**Story:** The token carries a tenant claim. They attach a policy to the repository, not to the routes, and then go looking for the hole — a preload, a nested filter, a count, an aggregate, a bulk delete.
**Must hold:**
1. Every read is narrowed in SQL, including counts and aggregates.
2. A create into another tenant is refused; the tenant column cannot be patched.
3. A foreign id answers 404, not 403.
4. A narrowing crosses a relation only where it was declared, and the declaration fails at start-up if the path is wrong.
5. A missing claim is a refusal, never a zero value.
6. An unscoped `DeleteAll` or `UpdateAll` is refused unless the policy says otherwise.
**Today:** 🟡 partial — the documented path is solid; three of the six have a named exception
**Evidence:** `_examples/auth-jwt-gin/main.go:100-108` is the whole policy and `main.go:148` puts it on the repository rather than the route; `test/integration/auth_jwt_test.go:205` (`TestAGatedRepositoryRefusesWhatTheTokenDoesNotCarry`) pins guarantee 5; `test/integration/gate_relscope_test.go` carries the control that proves the leak exists without the declaration. **Round 1 also cited `auth_jwt_test.go:112` for guarantee 5 and that was wrong** — `TestATokensTenantClaimNarrowsTheStatement` is guarantee 1's, and citing it made a one-test guarantee look like a two-test one. `[[UC-004]]`'s Status is **partially covered** with four gaps, and three of them land here. Guarantee 1: the count that produces a list's total is built from options that drop the per-request relation narrowing, so a list filtered through a relation can return narrowed items beside a total counted wider (UC-004 gap 4). Guarantee 2: `security.ScopeAttr` installs the row check, but a `Policy` written by hand with `Scope` set and `Inspect` nil — which is what "row-level security in one line" reads like — leaves a create into another tenant unconstrained and untested (gap 1). Guarantee 3: `[[D-008]]` itself records the exception — `Save` against an id naming a hidden row answers 403, which is the enumeration oracle this persona is hunting for. Two smaller ones: a frozen-field name is never validated against the model, so a typo silently protects nothing, and a caller-supplied limit turns a whole-set filtered write into a one-row inspection.
**If not ready:** the documented helpers are the safe path and the hand-written `Policy` is the trap. Guarantee 3's exception is a deliberate trade and stays; guarantee 1's count is a bug with a known site; guarantee 2 closes by refusing a `Policy` with `Scope` set and `Inspect` nil at `Gate` time, which is the same shape as the frozen-field validation and is one of the two things the kit below can run in one place instead of twenty.

### H-GENERAL-08 — The probe under the gate, and the oracle that needs no probe
**Who:** the same author, the month after, having wired the error subsystem H-GENERAL-17 asks for
**Wants:** the two features they turned on not to undo each other
**Story:** They follow one guide and get tenant isolation. They follow the other and get every violation on one form. Nothing at declaration tells them they now have a cross-tenant existence oracle.
**Must hold:**
1. Wiring the error subsystem does not widen what a caller can learn about rows the gate hides.
2. If it does, the declaration says so at start-up, where both facts are in scope.
3. A refused create does not confirm the existence of a row in another tenant even with no probe wired.
**Today:** ❌ missing
**Evidence:** the probe issues its own statement to decide which constraints the payload broke, and the gate's predicate is not in it unless `probe.WithScope` is passed (`crud/probe/options.go:70`). Nothing correlates the two: `probe.Full(cat)` is declared against a `crud.Meta` and never sees the policy, and the gate is a decorator that does not know a probe exists. The README's Sharp edges names the fact (`README.md:1600-1602`) and the `faults` sweep files it as row 5, `serious`. **Guarantee 3 is the half round 1 missed and the mitigation does not touch**: a create colliding on a unique index over some other column tells the caller a row exists in another tenant through the plain 409 the four named constructors already produce, with no probe involved at all. `docs/ai/usecases/modules/security/UC-004-isolate-tenants.md:138-142` records it — "Nothing in the gate addresses it and no test covers it" — and `:151-158` says the oracle half does not close, citing `[[D-042]]`. `security` says it is `errs`/`faults`' problem and `faults` says it is the gate's, which is exactly why it is here.
**If not ready:** they pass `probe.WithScope` and keep it in step with the policy, and they accept that it narrows the *unique* terms only — a foreign-key term reads the parent table and a restrict term the child, and the model's predicate names neither, so `probe.Skip` is the control there (`crud/probe/options.go:66-70`). Guarantee 3 has no mitigation in the tree; a consumer who follows the `WithScope` advice and believes they closed the oracle has closed one of two doors. What is missing for guarantees 1 and 2 is a correlation at `Bind`, where the chain already holds both.

### H-GENERAL-09 — Two roles, one PATCH
**Who:** anyone whose users table has a `role` column
**Wants:** an admin to be able to change `role` and `status`, and a user not to
**Story:** They mount `/users`, give admins a permission, and go looking for where a field is made writable per caller.
**Must hold:**
1. Field-level write permission can be expressed where every other per-caller rule lives.
2. Getting it wrong is a refusal, not a silent write.
**Today:** ❌ missing
**Evidence:** the gate's field freeze is a static list on the policy value — `Freeze(fields ...string)` returns `Policy{Immutable: fields}` (`crud/decorators/security/policies.go:183-185`) — so it is a property of the resource, not a function of the principal. `security.PerAction` is per *action*, not per field. Nothing in `crud/decorators/security` reads the principal to decide which columns an update may touch.
**If not ready:** two mounts with two DTOs and two policies, or a service type that diffs the DTO against the principal by hand — which is H-GENERAL-13's answer. **This one belongs to `security` and is filed here because `security`'s own sweep carries zero blocker-severity rows**, so moving it without filing it there loses it. The security decorator looks like it should cover this and does not, and the cost of writing it by hand and getting it wrong is privilege escalation through a documented route.

### H-GENERAL-10 — A column the API must never return, filter on, or sort by
**Who:** anyone with `password_hash`, `internal_notes`, `salary` or `stripe_customer_id` in the same table as the public columns
**Wants:** to name what the API exposes, and have everything else be invisible
**Story:** They mount `/users` on the stock path, then check what a curl can reach. Later they add a `Filterable` list and misspell one entry.
**Must hold:**
1. A column not named in the API's shape does not appear in the response body.
2. It cannot be filtered on, sorted by, selected or searched either.
3. Closing it is one statement per resource, not one per dimension.
4. A misspelled entry in the list that decides all this is caught at start-up.
**Today:** ❌ missing — every dimension is open by default, the body is the whole model, and the check that would catch a typo is called by nothing
**Evidence:** `crud/query/compile.go:70` — "Allow-lists of canonical paths. **Empty means \"anything the model maps\"**" — over `Filterable`, `Sortable`, `Selectable`, `Preloadable` and `Searchable`, all five unset by default. The response body is the model, unmodified, unless `WithTransform` is passed. So on a stock mount `GET /users?f=passwordHash:startsWith:%24argon2id` is a 200 with a row count: a blind search oracle over a column that was never meant to leave. The one lever is `WithTransform[M, ID, U](fn)` (`crud/http/crudnet/options.go:81-84`) — per resource, three type parameters, and it hides the column from the body while leaving it filterable, sortable and searchable. The struct-tag table (`README.md:1447-1456`) has `pk`, `auto`, `noauto`, `immutable`, `generated`, `version` and `-`; there is no `private`, and `-` drops the column from the repository entirely, which is not what a column the *server* reads wants. **Guarantee 4 is the largest unguarded declaration mistake in the tree.** `Config.Check` exists precisely because a misspelled entry is inert — its own error text says so: "%s names nothing on %s, so it exposes nothing and every request naming it is refused as the client's mistake" (`crud/query/compile.go:168-169`). And `grep -rn "MustCheck\|\.Check(" --include="*.go" . | grep -v _test` returns four lines, all inside `crud/query/compile.go` itself (`:147` is a doc comment, `:201-204` is `MustCheck` calling `Check`). No example calls it; `grep -rn "Check(" _examples/*/main.go` is empty; the README shows a `query.Config` at `README.md:879-885` and no check. So a typo in `Filterable` closes a field forever and answers every request naming it with the client's own 400.
**If not ready:** they write all five allow-lists and a presenter, per resource, and nothing tells them when one is missing or misspelled. On this file's own arithmetic that is the fact that will be missing on resource fourteen, and unlike a missing page cap nobody sees it happen. The cheap shape is a `db:",private"` tag — the model already declares what the server owns, so it can declare what the API may not name — or a blueprint-level `sqlrepo.Closed()` that flips all five at once. That is a challenge to `[[D-060]]` and is argued under "What it must not break". Guarantee 4 costs one line at the one place the config is already handed to something that knows the model.

### H-GENERAL-11 — A public list endpoint that cannot be asked for the whole table
**Who:** the team putting a resource on the open internet behind an API gateway
**Wants:** sensible ceilings without having to think of every abuse first
**Story:** They mount a list endpoint, then try to break it: a hundred-deep `or`, a thousand-element `in`, `?limit=1000000`, `?preload=comments`.
**Must hold:**
1. Nesting depth, condition count, preload count, `in` list length, sort length and search-field length are bounded whether or not the author says so.
2. A request cannot turn pagination off unless the endpoint allows it.
3. The number of rows one request can ask for is bounded by default.
4. The number of rows a *preload* can pull in is bounded too.
5. An unknown or disallowed field is a refusal naming the path, never a silently dropped clause.
**Today:** 🟡 partial — 1, 2 and 5 hold; **3 and 4 do not**
**Evidence:** the volume caps and their non-zero defaults are `[[D-060]]`, with `AllowUnpaged` closed by default and its own doc comment naming this exact hole (`crud/query/compile.go:54-68`: "MaxLimit is itself unset by default — so with both defaults, `?unpaged=true` on a public endpoint is a full table scan"). Guarantee 5 is `[[D-013]]`. Guarantee 3 fails: `crud/sqlrepo/blueprint.go:53-54` — "Zero disables the cap" — and `crud/options.go:252` only clamps when `maxLimit > 0`, so `sqlrepo.Define("users")` honours `?limit=100000000`. The `query` sweep files this as its row 2. **Guarantee 4 is the one round 1 hid**: `MaxPreloads` bounds how *many* relations a request names (default 16, `crud/query/compile.go:86`) and nothing bounds how many rows they bring, so `?preload=comments` on a page of 100 articles loads every comment of all hundred — `query`'s row 1, a blocker, and its "No workaround" note is accurate. It is the same class of risk as the missing `MaxLimit` default, on the same endpoint, and round 1's arithmetic ("1, 2 and 4 hold") declared guarantee 1 "now whole" in a way that read as though the volume question was answered. Guarantee 1 itself is whole: the search-field list is capped against `MaxSort` (`crud/query/compile.go:595-598`) and a preload's sub-compiler shares the condition counter (`compile.go:504-509`) — `docs/ai/usecases/Index.md` gap entry 20 still says both are open and is stale on those two halves.
**If not ready:** every resource must remember `sqlrepo.MaxLimit(n)`. All nine runnable examples do; the README quick start does not (`README.md:159`). For the preload there is nothing to remember — a consumer's only lever is to leave the relation out of `Preloadable`, which turns the feature off rather than bounding it. See the D-060 challenge under "What it must not break", which is a pair of changes rather than one.

### H-GENERAL-12 — Infinite scroll that does not shuffle
**Who:** the mobile developer paging a feed sorted by `published_at`, and the service on the other side of a `remote` resource walking the same cursor
**Wants:** a cursor that survives a write landing between two pages
**Story:** They ask for a page, get a `nextCursor` back, and send it.
**Must hold:**
1. A cursor the server minted is a cursor the server accepts.
2. It works over the columns a feed is actually sorted by, which are nullable.
3. A cursor walk that crosses a service boundary means the same thing on both sides.
**Today:** ❌ missing for the case that matters
**Evidence:** the `crud` sweep carries both halves as blockers. Row 1 is the **silent** one and is the worse of the two: a cursor sort over a `sql.Null[T]`/`sql.NullTime` column is minted *and accepted* and returns a page short of rows — "200, well-formed, fewer rows than exist, no error anywhere — and it is the exact model shape `[[UC-010]]` promises to adopt" (`docs/ai/usecases/modules/crud/Crud.md:553`, `crud/meta.go:503` against `crud/cursor.go:124`). Row 2 is the loud one: a paged read over a nullable sort column mints a `nextCursor` the next request refuses (`crud/sqlrepo/repository.go:238`). **Round 1 carried only the loud half**, which is the one every gorm and ent model is *least* likely to hit, since those models use exactly the `sql.Null*` types row 1 is about. Guarantee 3 is the general-remit half nobody owns: `remote` walks cursors over the wire (`remote/roundtrip_test.go` has no cursor-walk test at all — `remote` row 12), so a vv service paging another vv service is two implementations of the same token with no test between them.
**If not ready:** they sort by a non-nullable column, or page by offset and accept the shuffle. A tag freezes the wire behaviour of a token the server hands out, and the token crosses a service boundary the moment anyone uses `remote`, which is why guarantee 3 keeps this case here and not only in `crud`'s.

### H-GENERAL-13 — A business rule in front of a write
**Who:** a developer who has to check a quota before a create and write an audit row after it
**Wants:** to put a rule in the path without giving up the generated routes
**Story:** They write a small type that embeds the repository, override `Save`, and mount that instead. Nothing else changes.
**Must hold:**
1. The mount takes an interface, so their own type satisfies it.
2. Overriding one method keeps the other thirteen.
3. The same type mounts unchanged on every transport.
4. A refusal from their rule maps to a status the same way the library's own do.
**Today:** ✅ ready, with one trap
**Evidence:** `[[D-022]]`; `port.Repository` is the same type behind `crudfiber.Repository`, `crudgin.Repository`, `crudnet.Repository` and `crudgrpc.Repository`. `test/portmount/mount_test.go:395` (`TestTheServiceIsWhereTheRulesRan`) asserts the rules ran in the service and not in a binding. **The trap is placement.** Mounted *above* the repository as a service type, a plain `struct{ crud.Repo[…] }` is fine. Put the same shape in the `Bind` chain and it forwards no optional interface and no `Next()` — and the failing arrangement is the one where **the opaque decorator sits beneath the probe**, because `crud/decorators/faults/probe.go:92` resolves the source with `crud.SourceOf(next)`, which only walks downward. `crud/decorators/faults/probe_test.go:315-317` is that arrangement — `Docs.Bind(src, faults.Enrich[…](…), opaque[Doc, int64]())`, and `Chain` makes `mw[0]` outermost (`crud/repo.go:109-117`) — and it panics at `Bind`. **Round 1 stated this the other way round**, which told a consumer the safe placement was the failing one. `probe_test.go:321` calls that shape "the shape `crud.Base` exists to stop anyone writing by accident"; `[[D-061]]` is the failure it comes from, and `crud.Base` is the answer and it is one line.
**If not ready:** n/a. This is where H-GENERAL-05's validator, H-GENERAL-09's field-level permission and H-GENERAL-14's write hook all land, and it is the same ten lines three times. Note also the trap in H-GENERAL-15: a rule placed in a *handler* option rather than in the service is not in the path a worker takes.

### H-GENERAL-14 — Server-owned columns on every write
**Who:** anyone with `created_at`, `updated_at` and `created_by` — which is everyone
**Wants:** those three to be right after every write
**Story:** They tag the timestamps, write a row through the API, and expect it stamped.
**Must hold:**
1. A column the server owns cannot be set by a client.
2. It is correct after a create, after a patch, and after a batch write.
**Today:** 🟡 partial
**Evidence:** guarantee 1 is solid — `generated` and `immutable` are cleared from a create body (`[[UC-001]]` guarantee 6) and a DTO naming one is refused at `Define` time. Guarantee 2 holds when the *database* owns the value: `DEFAULT now()` plus a trigger, or MySQL's `ON UPDATE CURRENT_TIMESTAMP`, is right everywhere. Where the database cannot own it — `created_by` from the principal, a Postgres `updated_at` with no trigger — there is no repository-level write hook at all: `crud/sqlrepo/blueprint.go`'s `Setting` list has none, and the only hooks are the transport's. `crud.NowFunc` (`crud/meta.go:15-17`) is the library's own clock and stamps soft delete only (`crud/sqlrepo/repository.go:936`). **Round 1 carried a third guarantee — "it is correct whatever path the write came from" — which is H-GENERAL-15's, word for word.** One remedy closes both and it is stated there; this case no longer restates it.
**If not ready:** the remedy is H-GENERAL-13's — embed, override `Save`, ten lines — and the note that a decorator touching a patch DTO has to type-assert `dto any` because of `[[D-001]]`. A `sqlrepo` setting, or a shipped `hooks.OnWrite[M, ID]`, would close it and would be the same ten lines deleted from every project built on this.

### H-GENERAL-15 — The same tables from a worker with no request
**Who:** the author of a nightly job fleet that re-prices every tenant's rows
**Wants:** the same repositories, the same scoping and the same rules, from code with no HTTP request in it
**Story:** A cron process loops tenants, builds a context, and calls the same repository the API holds. It must not see across tenants by accident, it must not be locked out either, and it must not skip a rule the API enforces.
**Must hold:**
1. A caller identity can be put into a context without a transport.
2. A gated repository refuses when there is no identity, rather than reading everything.
3. A rule that runs on the API's write path runs here too.
**Today:** 🟡 partial
**Evidence:** 1 and 2 hold: `auth.WithPrincipal(ctx, p)` is exported and `auth.Claims` is a ready-made `Principal` (`auth/principal.go`), so a job builds `auth.Claims{Sub: "job:reprice", Attrs: map[string]any{"tenant": id}}` and the policy reads it unchanged; `[[D-055]]` makes the principal a context value on purpose. **Guarantee 3 is where it leaks**, and it is the one silent correctness hazard this sweep found for itself: the `BeforeSave` and `BeforeUpdate` hooks take a request (`crud/http/crudnet/options.go:102`, `:107`), the service only ever runs what a binding set (`port/service.go:158`, `:173`, `:204` read `cmd.Before`, and the only writers are the four handlers' `beforeSave`/`beforeUpdate`), and the pgx example's own comment recommends exactly that placement for a server-owned value (`_examples/pgx-fiber/main.go:103-111`). So the validator of H-GENERAL-05, the stamp of H-GENERAL-14 and any quota rule put there simply do not exist on the worker's path.
**If not ready:** put every invariant in a service type or a `crud.Middleware` and use the handler hooks only for things that genuinely are about the request. Nothing in the tree says that out loud; a sentence in the handler-option docs, and a different comment in the pgx example, would close most of it. A repository-level write hook would close it properly, and it is the same change H-GENERAL-14 asks for. This is in the blocker table: a rule the API enforces and the worker does not is worse than a missing feature, because both call sites read as correct.

### H-GENERAL-16 — Importing fifty thousand rows
**Who:** whoever gets the CSV upload, the backfill or the nightly sync — every application, month one
**Wants:** to write a large batch without leaving the framework or leaving the gate
**Story:** They read a file, build a typed slice and call `repo.InsertBatch`.
Pgx should become fast automatically without turning the import into a second,
less-safe architecture.
**Must hold:**
1. A batch too large for one statement is split by the dialect's bind budget and
   remains atomic.
2. The import goes through the same Gate, faults and consumer decorators as an
   ordinary repository write.
3. Pgx COPY is the default acceleration, while tables whose semantics require
   ordinary INSERT can opt out without leaving the typed API.
**Today:** ✅ covered
**Evidence:** `Repo.InsertBatch` is an optional typed repository capability and
is insert-only even for assigned keys. Sqlrepo derives table, columns and values
from metadata, preflights the complete input and selects native pgx COPY only
when the exact Source exposes the effect. A matching bound executor is supplied
as its target, so wrapper authority and transaction routing both survive.
Otherwise it renders bind-budgeted INSERT chunks and
runs a multi-statement plan through one ambient or owned transaction. `SaveAll`
and `Delete(ids...)` use the same budgeted atomic-plan machinery, so the original
one-statement ceiling is gone there too.

`security.Gate.InsertBatch` authorises Create and inspects a private copy of
every row; a scope-only policy without Inspect refuses. `faults.Enrich` preserves
operation and field attribution. An unknown repository decorator fails closed;
an unknown source wrapper gets SQL rather than a native effect tunneled
underneath it. Its `Exec` sees a direct one-statement plan; chunked work executes
on the transaction handle and needs transaction-aware or driver-level tracing.
`ReadWrite` explicitly routes native bulk to the primary.
`crud.PortableBatch()` chooses SQL for one call and `sqlrepo.PortableBatch()`
declares it for a repository. This is the RLS, rewrite-rule and special-encoding
escape hatch; magic remains the default.

**Historical finding, closed before the first release (FW-CORE-003).** The
original evidence correctly found unbounded `SaveAll`/`Delete` statements and a
source-level `crud.BulkInserter`/`CopyFrom` call that duplicated metadata,
disappeared behind wrappers, bypassed Gate/faults, ignored context transactions
and returned unclassified COPY errors. The old unmarked symbols were removed.
Only explicitly low-level `UnsafeBulkInsert*` / `UnsafeCopyFrom*` remain, for a
caller intentionally leaving repository policy and lifecycle.
**If not ready:** —

### H-GENERAL-17 — Errors a form can display, without a research project
**Who:** a full-stack developer whose signup form has three fields wrong
**Wants:** one response listing every violation, each with a machine code and the field it happened at
**Story:** They read the README's three-violation body, want exactly that, and go looking for the switch.
**Must hold:**
1. A refused write carries a machine code, not a driver sentence.
2. The response lists every violation the payload caused, or says it is incomplete.
3. No constraint name, table name or SQLSTATE reaches the client.
4. The status the README promises for that body is the status the code answers.
5. Turning it on is a decision, not a research project — and what is on by default is described correctly.
**Today:** 🟡 partial — 1, 2 and 3 hold; **4 does not**; 5 is four decisions in three packages, one of which does nothing, over prose that says the opposite of the code
**Evidence:** the code arrives free from a named constructor — the four named constructors `Postgres`/`MySQL`/`MariaDB`/`SQLite` (`crud/adapter/crudsql/crudsql.go:159-162`) route through `engine` at `:164-165`, which installs `sqlfault.New(name)`, while the generic `Open` at `:151` passes `classifier(nil, opts)` and does not — so the bare default from a named constructor is a 409 **with** a code and no field. `README.md:1218-1219` says the opposite in bold: "**None of this is on by default.** Wire nothing and a 409 is still a 409; you just get no `error_code` and no `field`." Half of that sentence is wrong for the four constructors every consumer uses, and it is the sentence that tells them whether to bother. Guarantee 3 is `[[D-044]]`, asserted against the whole captured corpus (`[[UC-015]]` guarantee 11). **Guarantee 4 fails on the first screen of the README.** `README.md:83` shows `POST /users → 422` over a body whose violations are `unique`, `foreign_key` and `check`; `errs/codes.go:68,70` map the first two to `KindConflict`, `port/kind.go:71-90` ranks Conflict (5) above Validation (6) and `worse()` at `:54-59` takes the lower rank, and `porthttp.StatusFor` renders Conflict as 409 (`port/porthttp/errors.go:62`). The real answer to that payload is 409, and 422 is the number a front-end team hard-codes. Guarantee 5: `faults.Enrich[M, ID](faults.WithProbe(probe.Full(cat)))` per resource, over a source rebuilt so the classifier can see the catalog. **It does not have to be last in the `Bind` list** — `probe.go:92` resolves the source with `crud.SourceOf(next)`, which walks the chain through `Next()`, and `probe_test.go:286` binds `Enrich` outermost with `security.Gate` beneath it on purpose. What `faults`' own package doc recommends (`crud/decorators/faults/faults.go:11-21`, under "# Order") is still right for two behavioural reasons `[[D-061]]` does not touch; what is false is the mechanical sentence in `docs/modules/en/faults.md:92-94` and `crud/decorators/faults/probe.go:51-54` — "the repository underneath is no longer `crud.Sourced` and the declaration refuses" — which is true only beneath a decorator with no `Next()`.
**If not ready:** they wire it per resource and get it right, or they ship the bare default and believe from the README that it carries nothing. And **the wiring the README prescribes for the vocabulary configures nothing on a generated route**: `crudnet.Errors()` renders only `if rec.err != nil && !rec.wrote` (`crud/http/crudnet/middleware.go:77-79`) and a CRUD route has already written through its own `errorHandler` (`handler.go:123-133`); `crudgin.Errors` checks `c.Writer.Written()` for the same result; the `port` sweep files it as row 1 and against **four** transports, not three. `README.md:1161` presents that call as how a catalogue is installed. See H-GENERAL-18 for the per-resource alternative, which has a second defect of its own.

### H-GENERAL-18 — The error body names the key the client sent
**Who:** the front-end developer reading `field` out of an error body to mark an input red
**Wants:** `authorId`, the name they sent, not `AuthorID`
**Story:** They post a body, get a violation back, and try to match its `field` against their form state.
**Must hold:**
1. The path names the request's own keys where the mapping is known.
2. A layer that would have to guess says so rather than inventing a path.
3. Installing a message catalogue does not cost the path mapping.
**Today:** 🟡 partial — 1 and 2 hold with a generated adapter; **3 does not**
**Evidence:** `[[D-043]]` (one hop per layer) and `[[D-050]]` (the generated inverse is total); `port.MustPathMap` refuses to boot when coverage lapses (`port/pathmap.go:154`). Without `cmd/vv -adapter` the renderer recognises names out of the raw body and declines on ambiguity, which is honest but partial. **Round 1 cited `test/bridge/fieldviolation_test.go:117` as the cost of forgetting the adapter and that was wrong**: `TestWithoutTheTagNameFuncEveryPathIsGoFieldNames` pins the cost of forgetting go-playground/validator's `RegisterTagNameFunc`, which is H-GENERAL-05's territory and a different start-up step; the package doc says it "holds the one assertion in the repository that imports a validation library". Guarantee 3 fails: `WithRenderer` is the only per-resource way to install a catalogue, and `build` consults `port.Hops(svc, mapper)` **only when no renderer was supplied** (`crud/http/crudnet/handler.go:126-133`, and the same lines in the other three bindings). A resource that gains a catalogue silently loses its generated path map and starts answering Go field names marked approximate. Filed as row 2 in the `port` sweep.
**If not ready:** they build the renderer themselves with `crudhttp.NewRenderer(porthttp.WithCodes(...), porthttp.WithMessages(...), porthttp.WithResolvers(port.Hops(svc, mapper))...)` per resource — which requires knowing that the default did that for them. Two advertised features cannot both be on at once and the one that turns off does so quietly, in the error body.

### H-GENERAL-19 — The morning after a migration
**Who:** the on-call engineer whose deploy renamed a column an hour before the release
**Wants:** the process to refuse to start rather than answer 500 on the first request that touches the column
**Story:** A migration lands, the model is not updated, and the service starts happily.
**Must hold:**
1. A model that no longer matches the table is detected at start-up.
2. The failure names the column.
3. The check knows which direction of drift is survivable, so it does not refuse to boot during a normal deploy.
**Today:** 🟡 partial — the table and the primary key are caught and named; every other column is not; guarantee 3 has never been asked
**Evidence:** wherever the probe is wired — which H-GENERAL-17 already treats as the recommended path — `probe.Full`'s `Declare` runs at `Bind` time and refuses when the model's table is not in the catalog (`ErrUnknownTable`, naming `meta.Table`) and again when the model's primary-key column is neither the whole primary key nor a unique key of its own (`ErrKeyDoesNotIdentify`, naming `tbl.Name` and `meta.PK.Column`) — `crud/probe/declare.go:42-49`, pinned by `TestADeclarationAgainstACatalogWithoutTheTableRefusesAtBindTime`. What is missing is the non-key column comparison, which is the common migration. `[[UC-014]]` states it out of scope for the generator (`docs/ai/usecases/modules/codegen/UC-014-keep-generated-artefacts-in-sync.md:71`), and the start-up checks that exist compare the *DTO* against the model (`port.MustCoverUpdate`), never the model against the server. Both usage guides suggest diffing against the **ORM's** metadata, which is a different question and unavailable to a project with no ORM. Guarantee 3 is new in this round and it changes the remedy: an equality check would refuse to boot the new pods during the expand phase H-GENERAL-20 describes, turning survivable drift into a failed deploy.
**If not ready:** they write it — `crud.SchemaOf[M]()` and `catalog.Load(ctx, src)` are both exported and the comparison is about fifteen lines over data the probe already loaded — or they find out from a 500. The comparison has to be directional: a column in the table the model does not name is fine, a column the model names and the table does not is fatal. What is missing is the ten lines and the sentence saying to, and H-GENERAL-02 is where the sentence goes.

### H-GENERAL-20 — Deploying a schema change on a running fleet
**Who:** the same engineer, mid-deploy, with half the pods on the old build
**Wants:** to know the order in which the migration and the deploy have to happen
**Story:** They add a column to the model and to the table, deploy, and watch. Next sprint they drop one.
**Must hold:**
1. There is a written rule for which of the migration and the deploy goes first, in each direction.
2. A constraint added an hour ago is reportable without restarting every process.
**Today:** ❌ missing for both
**Evidence:** vv names every mapped column in both directions — the INSERT names them all (`README.md:1547`: "vv writes every mapped column"), and the read is `"SELECT " + cols + " FROM " + table` built once at construction (`crud/sqlrepo/repository.go:45`), not `SELECT *`. So adding a column to the model before the migration lands breaks every write on the pods that rolled first, and dropping a column from the table before the model stops naming it breaks every read on the pods that have not. Expand-contract is therefore mandatory and in a specific order — add the column, deploy the model that names it, then later stop naming it, deploy, drop — and nothing in the tree says so. Guarantee 2: `catalog.Reloader` is written, tested and called by nothing outside `crud/catalog` (`load.go:331` is the only assertion that `*loaded` implements it), and `probe.Full` freezes its candidates at `Declare`, so a reload could not reach the probe anyway; the `faults` sweep files it as row 9, `serious`. The consequence is that the probe reports on the constraint set of whenever the pod started, and during a rolling deploy that is two different answers to the same request. The likelier direction is worse: a migration that renames or drops a constraint panics `faults.declare` at `Bind`, taking every endpoint on every pod that rolls (`faults` row 10).
**If not ready:** they learn the ordering rule by breaking a deploy, and they restart the fleet after a schema change. No module owns "the fleet during a migration"; that is why it is here. The ordering rule is one paragraph in a usage guide and it is the thing an on-call engineer needs at 3am — more than either of the start-up checks H-GENERAL-19 asks for.

### H-GENERAL-21 — Trash, restore and the tombstone
**Who:** the product team whose "delete" has to be undoable for thirty days and then permanent
**Wants:** the soft-delete feature the README sells to be reachable from the API it generates
**Story:** They declare `SoftDelete("DeletedAt")`, mount the resource, delete a row, and then need three things: a trash view, an undelete, and a hard delete for the GDPR request that arrives in month four.
**Must hold:**
1. A route can list deleted rows.
2. A deleted row can be restored.
3. A row can be removed for good.
4. Whoever can see the trash is a separate decision from whoever can see the resource.
**Today:** ❌ missing — the feature exists at the repository and nothing above it knows
**Evidence:** the declaration is one setting (`sqlrepo.SoftDelete("DeletedAt")`, `crud/sqlrepo/blueprint.go:87-112`) and `Delete`/`DeleteAll` stamp the column instead of removing the row (`repository.go:887`, `:905`, `:921-934`). Above that there is nothing: `grep -rln "SoftDelete\|softDelete" crud/http/ crud/rpc/ port/` is empty, and so is `grep -rn "IncludeDeleted\|OnlyDeleted\|WithDeleted" --include="*.go" crud/*.go crud/sqlrepo/*.go`. There is no repository verb for restore and no wire spelling for "include deleted", so `PATCH /:id` on a soft-deleted row is a 404, a restore has no route, and a hard delete has no verb at all. The blueprint's own doc comment shows the intended shape — a second `Define` of the same table with a different `Scope` (`blueprint.go:71-74`) — which means a second mount at a second prefix, seeing tombstones, needing its own policy, and sharing none of the first mount's configuration.
**If not ready:** they declare the resource twice and write the restore as a hand-written `UpdateAll` over the tombstone column — which the gate's frozen-field list will refuse unless they remember to unfreeze it there. Guarantee 3 has no answer at all: once a repository declares `SoftDelete`, nothing in it can issue a real `DELETE`. That is a missing verb, not a missing route.

### H-GENERAL-22 — The first production incident
**Who:** the engineer holding the pager, at 3am, with a 500 rate and an empty envelope
**Wants:** the cause of a failure the client is correctly not told about to be somewhere on the server
**Story:** A driver connection breaks, a scan fails, a presenter panics. The client gets a detail-free 500 — right — and they go looking for the reason.
**Must hold:**
1. A failure the client is not told about is recoverable on the server.
2. A slow statement can be attributed to an endpoint and a verb, not only to SQL text.
**Today:** ❌ missing for 1 on two of three HTTP bindings; ❌ for 2 everywhere
**Evidence:** `render` is "the one place a failure leaves this package" and it never touches `err` except to render it (`crud/http/crudnet/options.go:174-193`); the same shape is in `crudfiber`. Only Gin keeps the error, via `c.Error`. Filed as row 1 in the `crudhttp` sweep. `port.Logger(ctx)` is the correct seam and `[[D-062]]` holds — `grep -rn "port.Logger(" --include="*.go" . | grep -v _test` returns nine lines, all panics or encode failures — which is the evidence *for* the gap, not against it: the guarantee is true because almost nothing logs. Guarantee 2: wrapping a `crud.Source` is two methods and is documented and pinned (`crud/wrapsource_test.go:15-31`, including the `UnwrapSource()` that keeps the replica split alive under the wrapper), but the wrapper sees SQL text and args and nothing else — no model, no verb, no repository. `[[D-051]]` correctly refuses an OpenTelemetry satellite; what would close guarantee 2 without a dependency is an operation name on the executor context.
**If not ready:** for guarantee 1, one line in `render` and a row in `[[FL-013]]`, and a consumer choosing net/http because it costs no dependency stops also choosing an API with no post-mortem. For guarantee 2, they write the wrapper and reverse-engineer the verb from the SQL.

### H-GENERAL-23 — Testing the stack you actually mounted, without Docker
**Who:** a developer whose CI has no database and whose application has a gate, a probe and a service type
**Wants:** to test the thing they mounted, not the repository underneath it
**Story:** They write a test for the quota rule, drive it through the mounted route, and assert the status and the body — in a hundred milliseconds.
**Must hold:**
1. A repository can be driven with canned rows and no server.
2. A repository carrying the gate and a probe can be driven the same way, and the probe's extra statement is expressible.
3. A request can be driven through the mounted route with no database, asserting the status and the body.
**Today:** 🟡 partial — 1 is solid, 2 is unproven, 3 is a recorded gap
**Evidence:** `crud/crudtest/recorder.go:1-5` states the purpose and `Result.Err` versus `Result.RowsErr` (`recorder.go:30-40`) is the pgx-versus-`database/sql` distinction a lesser double would have collapsed. `[[UC-011]]`'s Status is **"covered for the repository, partially covered for the handler"**: the stand-in repository and every helper around it live in an internal test file and are not exported, so an application rewrites them. Guarantee 2 is the general-remit half and nothing in the tree answers it — a probe issues a *real* extra statement, so a recorder driving a probed repository has to have a row queued for it or it reads an empty result. Which brings the sharp edge: **running out of queued rows is not an error**, so a test asserting emptiness passes for the wrong reason — the failure this repository's own rule about vacuous tests exists to prevent.
**If not ready:** they export their own handler harness, or bind a real repository to a recorder and assert the SQL. Nothing prevents the second and nothing demonstrates it.

### H-GENERAL-24 — The relation-loading API the README opens with
**Who:** anyone whose first screen of the README sold them "a filtered, sorted, paginated, relation-loading HTTP API"
**Wants:** `?preload=author,comments.author` to work on their own schema, at a cost they can predict
**Story:** They add `rel` tags to a model an ORM generated, mount it, and send the preload from the README's own flagship document.
**Must hold:**
1. Declaring a relation on a model somebody else's tool wrote is possible without editing that model by hand.
2. A preloaded child obeys the same disclosure rules as the root — its private columns do not leak because they arrived one hop down.
3. What one preload costs in statements and rows is stated.
**Today:** ❓ unverified for 1, ❌ for 2 and 3
**Evidence:** nothing in this sweep exercises it end to end, which is itself the finding: `README.md:60` sells relation loading and `README.md:72-75` shows `"preload": ["author", "comments.author"]` in the flagship document, and no case in any general round has followed a consumer through declaring one. Guarantee 2 is the general seam: the allow-lists and the presenter are both root-model shaped — `WithTransform` takes `func(*http.Request, M) any` over the root `M` (`crud/http/crudnet/options.go:81-84`), and `Preloadable` gates *which relation* may be named, not which of its columns come back — so a child model carrying one of H-GENERAL-10's columns is serialised whole through a preload even where the root's presenter hides the same column. Guarantee 3 is H-GENERAL-30's second guarantee and is unstated anywhere: one preload level is one extra statement per level, chunked at 900 keys (`crud/preload.go:17`, `:221-222`), over every row of the page — with no row ceiling at all (H-GENERAL-11 guarantee 4).
**If not ready:** they find out what a preload costs by watching a p99. Guarantee 2 has no lever in the tree: the only way to hide a child's column today is to remove it from the child model, which the root's own reads may need. This case is marked ❓ where a later round should actually walk it; a sweep whose remit is the framework as a product should not leave the product's headline feature unexercised twice.

### H-GENERAL-25 — Reading a resource that lives in another service
**Who:** a platform team splitting a monolith, with a content service and an orders service
**Wants:** calling another service's resource to look like calling a local repository
**Story:** They point a resource at a base URL, write the same `Where` they would have written locally, and reconcile the two sides nightly with `GetAll`.
**Must hold:**
1. A filter written in Go reaches the far side as the same narrowing, and the local error branch still matches.
2. `GetAll` means every matching row on both sides, or refuses.
**Today:** ✅ covered, with an explicit enumeration boundary
**Evidence:** guarantee 1 is asserted by a real remote client against a real
binding. For guarantee 2, `remote.GetAll` now makes bounded List calls, follows
cursor edges, and reports malformed progress as `*remote.PartialResultError`.
The HTTP path is exercised against `MaxLimit(1)` and `MaxOffset(1)`, proving the
cursor transition. A cursorless custom list or DISTINCT projection without the
primary key needs a sufficient endpoint `MaxOffset`, and the result is an
enumeration rather than a cross-page snapshot.
**If not ready:** —

### H-GENERAL-26 — Moving reads onto a replica, through a wrapper
**Who:** the same team, the week the primary's CPU graph stopped being flat, who already wrote the timing wrapper from H-GENERAL-22
**Wants:** reads on the replica and no correctness surprise
**Story:** They open a second handle, wrap the pair, wrap that in their own timing `Source`, and change nothing else.
**Must hold:**
1. Reads go to the replica and writes to the primary; a read that decides a write does not; a read inside a transaction stays there.
2. The split survives a consumer's own `Source` wrapper.
**Today:** 🟡 partial — 1 is proven; 2 is the one erasure that fails silently
**Evidence:** guarantee 1 is `crud.ReadWrite`, `vvdb.OpenReadWrite`, `dbpgx.ConnectReadWrite`, `[[D-032]]`, and `test/integration/replica_test.go:24`, `:96`, `:138`, `:187`. Guarantee 2 is the seam: `[[D-061]]` names three optional interfaces a wrapper erases, and `ReadSourcer` is the one whose loss reports nothing — the transaction beginner disappearing produces an error, the replica split disappearing produces correct answers from the primary and a CPU graph that never flattens. `crud/wrapsource_test.go:15-31` is the only working example of a wrapper that keeps it, and it lives in a test file. **Round 1 attributed this to an `adapters` blocker about "no provided `Source` wrapper" and no such row exists.** Its nearest historical comparison was the old bare `crud.BulkInserter` assertion; FW-CORE-003 closed that separate effect seam by making `ReadWrite` forward explicitly and making an unknown wrapper select portable SQL rather than lose the repository operation. `ReadSourcer` remains a read-capability walk and this case remains partial.
**If not ready:** they read `[[D-061]]`, or they do not. The general-remit fact is that H-GENERAL-22's remedy and this case's guarantee are the same wrapper, written by the same person, and one of them silently undoes the other.

### H-GENERAL-27 — Adopting it one resource at a time
**Who:** the maintainer of a five-year-old service with handlers they are not allowed to break
**Wants:** to move one endpoint over and leave the rest alone
**Story:** They pick the least interesting resource, mount it beside the existing handlers, share the connection and the transaction, and see whether anything breaks.
**Must hold:**
1. The library never opens a connection; the application's handle is used as-is.
2. A transaction the existing code opened can be joined, so both halves see each other's uncommitted writes and roll back together.
3. Where a process talks to two databases, a joined transaction does not capture the wrong one.
4. The ORM's own builders keep working beside it.
**Today:** ✅ ready
**Evidence:** `[[D-057]]`, `[[D-082]]`, [[UC-005]] and [[UC-012]]. The usage-guide
path binds the canonical source and foreign executor in one call. Live
database/sql and pgx rollback tests refuse both source-less and
transaction-as-source mistakes before touching the pool; the two-database test
keeps another repository on its own source. The unsafe legacy capture remains a
separate control through `WithUnsafeExecutor`. ORM-side hooks still do not run
for statements this library issues ([[D-017]]).
**If not ready:** —

### H-GENERAL-28 — Upgrading eleven modules at once
**Who:** the person who runs `go get -u ./...` on a Friday
**Wants:** the library and every binding to move together, or to be told they did not
**Story:** They are on the library, the Gin binding, the pgx adapter, the JWT provider and the pgx pool helper. One of those releases. They update.
**Must hold:**
1. One version number means the same thing in every module.
2. A mismatched pair is detected, not discovered.
**Today:** 🟡 partial — 1 is enforced at release time, 2 is not enforceable
**Evidence:** `scripts/release.sh` refuses to tag unless every satellite's `go.mod` already requires the library at exactly the version being released, and the tags are pushed atomically with the library first. Guarantee 2 is honest about why: "MVS has no upper bound, so lockstep **cannot** be mechanically enforced… the dangerous direction — old binding, new library — is what a bare `go get -u` produces" (`docs/roadmaps/Roadmap.md`, item 4). `retract` is the only lever and there is nothing yet to retract. **And the module count changes inside the first release cycle**: `errs` becomes its own module immediately after the tag — decided, and blocked from happening before it for module-graph reasons (`Roadmap.md:57-68`, `[[D-036]]`). So `retract` has to cover a twelfth module that did not exist at v0.1.0.
**If not ready:** a consumer who updates one module and not the others gets a compile error if they are lucky and a behaviour difference if they are not. **Round 1 proposed "a one-line `const Version` compared in an `init`" and that remedy is worse than the disease**: across twelve modules it panics the process on any skew, including the harmless direction — a patch release of the Gin binding against last week's library. The cheap version is a **floor, not an equality**, aimed at the one direction MVS cannot already produce: each satellite asserts the library is at least the version it was built against, and reports rather than panics — `port.Logger(ctx)` at start-up, or a `Check() error` a consumer calls.

### H-GENERAL-29 — Handing the API to the front-end team
**Who:** the developer who now has to tell four TypeScript developers what the endpoints are
**Wants:** a machine-readable description of the routes, the filter grammar and the error envelope
**Story:** The API exists and is uniform. They want to hand over a schema and generate a client, rather than a paragraph and a curl.
**Must hold:**
1. There is a description of the route set a client generator can read.
2. The filter grammar is described somewhere other than prose.
3. The error envelope is a documented type.
**Today:** ❌ missing for 1 and 2, ✅ for 3
**Evidence:** nothing in the tree generates or consumes an OpenAPI document — no package, no generator, no doc. `grep -rIln "openapi\|swagger" .` returns six paths and none of them is code: `docs/roadmaps/2026-08-26-1522-product-roadmap.md`, this file, `test/go.mod`, `test/go.sum`, `_examples/go.mod` and `_examples/go.sum`, where `github.com/go-openapi/inflect` is an ent transitive dependency. (Round 1 said the grep returns two paths; it returns six, and a document that corrects the previous round for exactly this kind of claim has to get its own right.) The envelope is a real Go type with a parser on both sides (`porthttp.Envelope`, `porthttp.ParseEnvelope`) — though `ParseEnvelope` has no test anywhere, which the `port` sweep files as its row 6. gRPC has method names but deliberately no reflection, because a resource generic over its model has no compiled descriptor (`[[D-052]]`) — which the `crudgrpc` sweep files as a blocker of its own, since the example tells the reader to use grpcurl.
**If not ready:** the front-end team gets prose and a shared example. Every route set is mechanically derived from one model and one config, so a generator has everything it needs — the route table is fixed, the grammar is `query.Request`, and the allowed fields are already in `query.Config`. It is the largest missing piece that is *not* a correctness question.

### H-GENERAL-30 — Deciding to depend on it
**Who:** the staff engineer in the review where the dependency is proposed
**Wants:** three numbers: what it costs per request in CPU, what it costs per request in round trips, and what a v0 tag promises
**Story:** They read the README, like it, and ask what the reflection and metadata cost against the hand-written SQL it replaces, how many statements one list costs, and whether v0.2 will break them.
**Must hold:**
1. There is a measurement of the per-request overhead somewhere the consumer can find.
2. The number of statements one request costs is stated, because that is what sizes a pool.
3. There is a written statement of what a v0 tag promises and how a break is announced.
**Today:** ❌ missing for all three
**Evidence:** `grep -rn "func Benchmark" --include="*.go" .` returns nothing — there is no benchmark anywhere in the tree, so the cost of the reflection, the metadata walk and the option compilation against hand-written SQL is unmeasured and unstated. Guarantee 2 is derivable and written down nowhere: a list is one SELECT plus one COUNT, except that the COUNT is skipped when the page is a short first page, when the request is unpaged, or when `NoTotal` is set — which a cursor walk sets implicitly (`crud/sqlrepo/repository.go:210-226`, `crud/options.go:121`, `:131`); each preload level adds one statement, chunked at 900 keys; a gated `Save` carrying an id adds a read (`crud/decorators/security/security.go:476` calls `saveTarget` before deciding create versus update); and a refused write with the probe wired adds one more. So a list with three preloads is five statements and a gated update is three, and a consumer who sizes `max_open` from request rate alone exhausts the pool the first time a client sends `?preload=`. Guarantee 3: `docs/api/surface.md` exists as an exported-surface baseline and `CLAUDE.md` says a disappearing line is a breaking change, but that is a note to a maintainer, not a promise to a consumer. H-GENERAL-28 answers "do the modules agree with each other" and not "will v0.2 break me".
**If not ready:** the engineer benchmarks it themselves or takes the risk. All three are release-shaped, none is owned by a module, and one benchmark of `GetByID` and `Get` with a recorder source costs an afternoon. Guarantee 2 costs a paragraph.

## The DX this should have

### The call site

The error subsystem needs the source built in two steps, because the classifier
is baked into the concrete adapter at construction (`crudsql.WithFaults` is a
`crudsql.Option`) and `crud.Source` is `Executor + Dialect()` and nothing else —
there is no seam to attach one through afterwards. Written out with the error
returns nobody may elide, the honest sketch is eleven lines before the first
mount:

```go
db := vvdb.MustOpen(&cfg.DB)                        // the application still owns the handle

cat, err := catalog.Load(ctx, crudsql.Postgres(db))
if err != nil {
    return err
}
src := crudsql.Postgres(db, crudsql.WithFaults(
    sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))

kit, err := vvkit.New(src,
    vvkit.Errors(cat),                     // the probe and the per-resource Enrich
    vvkit.ScopeAttr("TenantID", "tenant"), // every resource, narrowed by the claim
    vvkit.Bound(house),                    // the house query limits
)
if err != nil {
    return err
}

articles := vvkit.Repo(kit, store.Articles)  // specs.Repo[Article, int64, ArticleUpdate]
crudgin.Serving(vvkit.Service(kit, store.Articles)).Mount(r, "/articles")
crudgin.Serving(vvkit.Service(kit, store.Authors)).Mount(r, "/authors")
```

`vvkit.Repo` is the primitive and `vvkit.Service` is defined in terms of it,
because the decorated repository is the value a worker, a Criteria query and a
transaction all need. It returns `specs.Repo[M, ID, U]`, which embeds
`crud.Repo` (`crud/decorators/specs/executor.go:18-24`), so the Criteria API is
in the value rather than being a fifth thing to remember — which is the only
version of this proposal that closes the omission H-GENERAL-04 counts five of.
`store.Articles` is a `*sqlrepo.Blueprint[Article, int64, ArticleUpdate]`, so M,
ID and U are inferred from it and neither call spells a type parameter.

**`vvkit.New` returns an error because it is the place four checks nobody runs
today can run once instead of twenty times.** It is the only value in the design
that holds both the scope and the error wiring, so:

- `vvkit.Errors(cat)` derives `probe.WithScope` from whatever
  `vvkit.ScopeAttr(...)` declared, and `vvkit.New` refuses a kit that carries a
  scope descriptor and a probe that would not be narrowed by it. That is
  H-GENERAL-08's "correlation at `Bind`", arriving one layer up. Without it the
  kit does not close blocker 9, it mass-produces it. **The residual has to be
  said too:** the narrowing reaches the model's own table only, so a foreign-key
  term still reads the parent and a restrict term the child
  (`crud/probe/options.go:66-70`), and `probe.Skip` stays the consumer's call.
  Nothing anywhere closes H-GENERAL-08's guarantee 3.
- The scope is **opt-out, not opt-in**. A resource built through the kit with no
  scope descriptor and no `Without(vvkit.Scope)` is refused at kit-build time.
  H-GENERAL-06 is why: today a mount with no policy is the shortest thing to
  write and the most dangerous, and a kit that leaves that arrangement silent
  has reproduced it twenty times.
- `vvkit.New` runs the validation H-GENERAL-07 asks `Gate` to run: a `Policy`
  with `Scope` set and `Inspect` nil is refused, and frozen field names are
  resolved against the model.
- `Service(kit, bp).Query(cfg)` calls `cfg.Check(bp.Meta())` and returns the
  error. `bp.Meta()` is already exported (`crud/sqlrepo/blueprint.go:242`), and
  this turns the documented-but-uncalled method of H-GENERAL-10 guarantee 4 into
  a guarantee. It is the cheapest thing in the whole proposal.

**Permissions are not in the sketch, and that is a decision.** Round 1 had a
`vvkit.Permissions(perm)` keyed by model name. That is a lookup by identity and
`[[D-037]]` forbids it in as many words; see "What it must not break". The only
version that survives is derivation from the `crud.Meta` the blueprint already
carries — `note:read` from `Note` — and that has to be argued on its own, not
smuggled in as a map.

### Turning one knob

Per-resource refinements are **methods on the value `Repo`/`Service` returns**,
not options in a variadic list. That is not a style choice: Go infers a
function's type arguments from its own arguments, and nothing in `Query(cfg)`
mentions M, ID or U — `crud/http/crudnet/options.go:29-42` explains this in the
existing code — so a free `vvkit.Query(cfg)` would have to be written
`vvkit.Query[Article, int64, ArticleUpdate](cfg)`, which is the exact cost the
proposal exists to delete. A method's receiver already carries all three.

```go
crudgin.Serving(
    vvkit.Service(kit, store.Articles).
        Query(house.With(query.Config{Preloadable: []string{"Author"}})).
        Middleware(audit.Log[Article, int64](l)),   // a consumer's own decorator
).Render(house.Render...).Mount(r, "/articles")

// the resource that is the tenant, so it opts out of the one element it cannot have
crudgin.Serving(vvkit.Service(kit, store.Tenants).Without(vvkit.Scope)).Mount(r, "/tenants")

// where the resource needs real code, it stays inside the kit rather than leaving it
crudgin.Serving(
    vvkit.Service(kit, store.Articles).Over(
        func(r specs.Repo[Article, int64, ArticleUpdate]) port.Repository[Article, int64, ArticleUpdate] {
            return articleService{Repo: r, quotas: quotas}
        }),
).Mount(r, "/articles")
```

Four things in that snippet are load-bearing and round 1 had three of them wrong.

**`Render` is a method, not `crudgin.Defaults(...)`.** Round 1 proposed
`crudgin.Defaults(opts ...porthttp.RenderOption) []Option[M, ID, U]` and called
it small and legal. It is legal and it is not small: nothing in
`...porthttp.RenderOption` mentions M, ID or U, so every call site reads
`crudgin.Defaults[Article, int64, ArticleUpdate](house...)` — three type
parameters per resource, which reintroduces blocker 19 at the exact call site
blocker 3 is meant to fix. A method on the handler carries all three in its
receiver.

**`Without(...)` takes the descriptor identities the kit was built from, rather
than one `No…` method per element.** Round 1 sketched `.NoScope()`, and with
five elements that is `NoScope`, `NoProbe`, `NoPermissions`, `NoBounds`,
`NoSpecs` — an exported method per element, growing with the kit, and a
twentieth resource that reads as a chain of negatives. One `Without` keeps the
opt-out surface fixed however many elements the kit grows, and it keeps the
descriptor set the closed thing the D-037 argument depends on.

**`Over(...)` keeps the resource inside the kit.** Round 1's escape hatch was
`crudgin.Serving(port.NewService[Article, int64, ArticleUpdate](svc))`, which
abandons the short path in two ways at once: it spells all three type
parameters, and `port.NewService` takes its own `ServiceOption`s
(`port/service.go:82`), one of which is `port.WithQuery` (`port/service.go:54`),
so `vvkit.Bound(house)` is silently gone on the one resource that has
hand-written rules. That is this file's own failure mode — a per-application
fact missing from one resource, with nothing failing — reproduced inside the
proposal's own snippet. `Over` takes a function from the decorated repository to
a `port.Repository`; the receiver carries M, ID and U, and the kit still applies
the bounds.

**The order the decorators end up in is fixed and stated**, because it is
observable: `Bind` puts `mw[0]` outermost (`crud/sqlrepo/blueprint.go:244-245`).
The kit builds `Bind(src, <per-resource Middleware>, Gate(policy), Enrich(...))`
— `faults.Enrich` innermost, which is what its own package doc recommends and
for the two behavioural reasons `[[D-061]]` did not touch; the gate above it;
the consumer's own decorator outermost. So `.Middleware(audit.Log(...))` sees
calls the gate will refuse. An audit decorator that must see only permitted
calls cannot be expressed with one method and has to build that resource without
the kit — which is a real limit and belongs in the doc rather than in a footnote.

`house.With(...)` is a merge helper that does not exist today; the proposal needs
it, on `*query.Config` in `crud/query`, or the snippet becomes a plain struct
copy. Naming it matters because `Config.Check` binds allow-lists to one model's
`crud.Meta` (`crud/query/compile.go:152`), so "share the house config" is true of
the numeric half and false of the allow-list half.

Independently of any kit, the per-handler options should be reachable as methods.
The `With` prefix stays, because `Query`, `List`, `Create`, `Update`, `Replace`,
`Delete`, `BulkDelete`, `CountGet`, `CountPost` and `GetByID` are **already
exported route handlers** on `*HandlerFor` in all three HTTP bindings
(`crud/http/crudgin/handler.go:168-371`) and on `*HandlerFor` in `crudgrpc`
(`crud/rpc/crudgrpc/handler.go:90-244`), exported on purpose so a project on
chi, gorilla/mux or httprouter can register them one by one:

```go
crudgin.New(repo).WithQuery(cfg).BeforeSave(stamp).ReadOnly().Mount(r, "/articles")
```

against today's:

```go
crudgin.New(repo,
    crudgin.WithQuery[Article, int64, ArticleUpdate](cfg),
    crudgin.BeforeSave[Article, int64, ArticleUpdate](stamp),
    crudgin.ReadOnly[Article, int64, ArticleUpdate](),
).Mount(r, "/articles")
```

It is a rename of a public route handler in **four** modules — `crudgrpc` carries
the same three-parameter options (`crud/rpc/crudgrpc/options.go:60`, `:66`,
`:72`, `:86`) and the same exported verb names — and the triplet rule makes it
four changes plus a row in `[[FL-013]]`. Round 1 said three and called the change
"additive, breaks nothing".

**And the methods must not disarm the one guard that exists.** `Serving` refuses
a service-shaped option at construction (`port/rules.go:91-98`, called from
`crud/http/crudgin/handler.go:104-107`) because an ignored `WithQuery` "would
leave an API accepting everything while its author believed it was bounded, and
that is exactly the failure `[[D-021]]` says must happen at start-up". A builder
method sets the field *after* that check has run, so
`crudgin.Serving(svc).WithQuery(cfg)` would panic today and silently do nothing
tomorrow. Either every method on a `Serving` handler re-runs
`RefuseServiceOptions`, or `Serving` returns a distinct type carrying only the
transport-shaped methods so `WithQuery` is a compile error. The second is better
and it is the more invasive of the two.

### Why this shape

The per-resource cost is what a consumer pays twenty times, so it is the number
to optimise; the per-application cost is paid once and can afford words. Today
the ratio is backwards — the application-level facts have no home, so each is
restated at every resource, and the twentieth resource is where one of them is
missing.

**The honest arithmetic, against the fullest example in the tree.** One resource
in `_examples/auth-jwt-gin/main.go` is the model struct (8 lines, 59-66), the DTO
(4, 71-74), the `sqlrepo.Define` block (5, 76-80), the policy (9, 100-108), the
`specs.Executor(...Bind(...))` line (148), and the mount (7, 153-159) — call it
34. The kit removes the `Bind` line, two of the policy's nine, and two of the
mount's seven: the three allow-list lines are irreducible content and the mount
around them goes from four lines to two. That is **about six of thirty-four**,
not fourteen. It becomes about thirteen only if a naming convention derives
`note:read` from `Note` and deletes the seven-line `security.PerAction` map —
so **the derivation is the load-bearing half of the proposal, not a footnote**.
Round 1 counted the best case as the number and called it fourteen; the section's
credibility rests on this arithmetic being the pessimistic one.

What matters more than the total is *which* lines: the ceremony is where the
silent omission happens, and the irreducible content — the model, the DTO, the
table name, the allow-lists — is most of the twentieth resource either way. The
strongest argument for the kit is not the line count at all. It is that
`vvkit.New` is a place to run four start-up checks that today are documented and
never called.

**The kit's elements have to be data, not functions.** Go cannot store an
uninstantiated generic function in a value, so `vvkit.ScopeAttr("TenantID",
"tenant")` is a descriptor that a generic `vvkit.Service[M, ID, U]` type-switches
on and turns into `security.ScopeAttr[M, ID](…)`. That closes the set: the kit
can carry the decorators the library ships and no others, and a consumer's own
decorator goes in per resource, where it infers from its argument
(`.Middleware(audit.Log[Article, int64](l))`). It is also the half of the
proposal `[[D-037]]` has to be asked about, below.

**The alternatives cost more than they look.** A registry that resolves a
repository by model type is the shape everybody reaches for and `[[D-037]]`
forbids it. Code generation for the wiring is `docs/roadmaps/Roadmap.md:137-140`,
and it already half-exists: `cmd/vv -binding net` writes a per-resource
`MountArticle(mux, prefix, svc, opts ...crudnet.Option[…])`
(`_examples/example/blog/vv_gen.go:144`) that absorbs the mapper and the three
type parameters. **The kit and the generated mount are complements, not
rivals**, and the proposal has to say so: the generated `MountX` takes a
`port.Service`, so `vvkit.Service(kit, store.Articles)` is what a project with
both passes into it. Extending the generator to the satellites is what the
roadmap item refuses, because it would put a satellite import in generated
output; that argument does not reach the kit, which imports no binding.

**Where it lives is an open question and part of the proposal.** Not under
`utils/`: the one boundary that directory has is that nothing beneath it imports
`crud/`, `auth/`, `port/` or `remote/`, and `make check-utils` holds it. A new
top-level `vvkit/` is a directory that is not a subsystem, which is a question
for `[[D-058]]`, and the `vv` prefix is a question for `[[D-035]]`, whose rule is
that a prefix appears only to break a collision — nothing collides with `kit`.
**And there is a third question round 1 did not ask:** `[[D-037]]` is written for
a composition-root package called `app` that does not exist yet, and this is a
composition-root package that does not exist yet, in the same repository, under a
different name. Either they are one package — in which case D-037 governs this
sketch directly and has to be amended rather than argued around — or they are two
and the owner has to say where the line is. An owner cannot accept or refuse a
package without a location and without knowing whether it is the one already
reserved.

### What it must not break

- **[[D-037]] is deliberately challenged, and it is the challenge that decides
  whether this package can exist.** Two of D-037's three forbidden shapes are in
  the sketch. The first is "Do not accept `...any` and type-switch it. That is
  the same lookup wearing a different shape" — which is exactly what
  `vvkit.New(src, vvkit.Errors(cat), vvkit.ScopeAttr(…))` plus a generic
  `Service[M, ID, U]` that switches on the descriptors does. The argument for
  it: D-037's subject is *resolving a dependency* — being handed a type and
  finding the value — and nothing here is found. The descriptors are values the
  consumer wrote at a call site they wrote, the set is closed by the kit itself,
  and the type switch constructs a typed decorator rather than retrieving one.
  Go leaves no third shape: an uninstantiated generic function cannot be stored
  in a value, so "data plus a generic constructor" is the only way to say
  `security.ScopeAttr[M, ID]` once for twenty models. If the owner reads D-037 as
  covering that, this package cannot be written in Go as sketched and the
  proposal dies there — which is worth knowing before anything is built.
  The second forbidden shape is round 1's `vvkit.Permissions(perm)` keyed by
  model name — "Do not key anything on `reflect.Type`. Not a map, not a slice
  searched by type" — and there is no argument for it, which is why it is out of
  this round's sketch. Derivation from `crud.Meta` at the call site is the only
  version that does not key anything.
- **[[D-060]] is deliberately challenged in two places, and each challenge is a
  pair of changes.** *First:* `sqlrepo.MaxLimit` should gain a non-zero default
  and `MaxLimit(0)` should become the explicit way to say unlimited. That
  contradicts D-060's boundary, which put page size outside the set of volume
  caps that got defaults. The argument is D-060's own applied one row further:
  `BulkCap` was changed for exactly this reason. **The second half is what D-060
  wrote down in advance**: "a non-zero `MaxLimit` silently truncates a remote
  `GetAll`, which is worse than refusing it" (`crud/options.go:242-245` returns
  `maxLimit` rows and no error). A default without a loud clamp trades a
  public-endpoint memory risk for a cross-service correctness risk; propose the
  pair or neither. *Second:* H-GENERAL-10 proposes a blueprint-level
  `sqlrepo.Closed()` and a `db:",private"` tag, and D-060's "What is deliberately
  still open" records the open allow-list defaults as decided rather than missed.
  The proposal does not break D-060's "Do not close the allow-list defaults one at
  a time" — `Closed()` flips all five together, which is the posture D-060 says
  they are. What it challenges is D-060's *reason*: "The exposure is bounded by
  what the model maps, which the consumer wrote." A model maps `password_hash`
  because the server reads it, not because the API should be able to sort by it.
  D-060's other reason — that a closed default would make the first `Define` of
  every model a wall of allow-lists — is exactly why this is an opt-in statement
  and not a changed default. Either way the outcome belongs in D-060, including a
  rejection.
- **[[D-021]]** — the builder methods must not turn `Serving`'s start-up refusal
  into a silent no-op; see the paragraph under "Turning one knob".
- **[[D-057]]** — the kit takes a `crud.Source`, never a DSN and never a config.
- **[[D-033]]** — the kit cannot import a binding, so it produces a
  `port.Service` and each transport mounts it. That is why the sketch says
  `crudgin.Serving(vvkit.Service(…))` rather than `vvkit.Mount(…)`, and it is
  the whole of why the kit cannot close H-GENERAL-04's renderer element: a
  package that cannot import `crudgin` cannot produce a `crudgin.Option`. **Round
  1 also blamed a panic in `crudgin.Serving` and that was wrong** —
  `RefuseServiceOptions` switches on `Query` and `AllowClientID` only
  (`port/rules.go:91-98`) and a renderer is neither.
- **[[D-016]]** and **[[D-048]]** — it is not in package `crud` and it does not
  join the contract manifest.
- **[[D-001]]** — the decorators it builds must still be the two-parameter kind.
- **[[D-061]]** — `vvkit.Repo` returns `specs.Repo[M, ID, U]`, a struct embedding
  `crud.Repo`, and a consumer's service type embeds *that* rather than a
  `port.Repository` interface, because embedding the interface is the erasure
  D-061 records. Round 1's bullet said "`Wrap` takes `crud.Repo` (the struct)";
  `crud.Wrap` takes a `Core` (`crud/repo.go:68`), so the bullet named the wrong
  function.
- **The window is now.** Both DX proposals are free before the first tag and
  neither is free after it. The builder methods change an exported surface in
  four modules, and `make api` maintains `docs/api/surface.md` precisely so a
  disappearing line reads as a break; the `MaxLimit` default changes behaviour a
  consumer may have come to rely on.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| One resource mounted safely in one statement | The one-line form `crudnet.New(repo).Mount(mux, "/products")` exists and is the form blockers 4, 5 and 6 are about: open in five query dimensions, whole model in the body, `?limit=100000000` honoured, and writable by anyone the auth layer let through. The shortest *safe* mount is a policy (9), a `Bind` line, three allow-lists, a `MaxLimit` and a presenter — about 16 lines | large |
| One resource with bounds, in one statement | 7 lines, and three type parameters per option (`_examples/pgx-fiber/main.go:91-97`) | small |
| The decorator stack declared once | Nothing. `Bind(src, …)` and `specs.Executor(…)` per resource | large |
| The error vocabulary and messages said once | Once per handler via `WithRenderer[M, ID, U]` — which also discards the generated path map — plus `Errors()`, which reaches no generated route on any of the four transports, plus the auth middleware. The default renderer is unexported | large |
| The query bounds said once, refined per model | A shared `*query.Config` works for the numeric limits; the allow-lists are validated against one model by `Config.Check`, so they are copied per model — and nothing calls `Check` at all | small to write, large in consequence |
| A resource that exposes only what I named | Nothing. `Filterable`, `Sortable`, `Selectable`, `Preloadable` and `Searchable` all mean "everything" when unset, the body is the whole model unless `WithTransform` is passed, and a typo in any of the five is inert | small to write, large in consequence |
| A safe default page size, and a bounded preload | None for either. Every resource must remember `MaxLimit`, and giving it a default needs the remote-`GetAll` half too; a preload has no row ceiling and no lever | small to write, large in consequence |
| Refuse a bad payload before the database sees it, in one envelope with what the database would have said | A per-transport `Before` hook, absent on the worker path, that returns before the write — so the two halves cannot both appear. `errs.FromFieldViolations` exists and nothing calls it | large |
| Turning on the full error subsystem as one decision | ~8 lines, the source constructed twice around `catalog.Load`, one per-resource decorator, and a per-resource `probe.WithScope` if the resource is gated | medium |
| Server-owned columns on every write path | A hand-written middleware per model, or a database default | small |
| A rule that holds on the API and on the worker | The handler hooks take a request; nothing says so | small to write, large in consequence |
| Test the mounted stack — gate, probe, service type — with no database | The stand-in repository and its helpers live in an internal test file; an application rewrites them | medium |
| Trash, restore and hard delete on a soft-deleted resource | A second `Define` at a second prefix with its own policy; restore is a hand-written `UpdateAll`; hard delete has no verb | large |
| A 500 whose cause the operator can read | Gin only. One line in `render` on the other two | small |
| Statement timing attributed to an operation | A hand-written `Source` wrapper that sees SQL text and nothing else, and must carry `UnwrapSource()` | medium |
| A machine-readable API description | Nothing | large |
| A number for what it costs per request, in CPU and in statements | Nothing — no benchmark in the tree, and the statement count is stated nowhere | small |

**Overall.** For one resource this is as short as the README claims, and the
short path really does extend rather than stop: a service type, a mapper, a
different transport and a remote resource all slot into the same sentence, and
that is unusual. It gets wordy at exactly one place and it is the place an
application lives — the assembly. Three type parameters per handler option is
the visible half; the invisible half is that every application-level fact has to
be restated per resource, so the twentieth mount is where the probe, the
renderer, `specs.Executor`, the page cap or the allow-lists are quietly missing,
and nothing fails when they are. Customising means extending rather than
starting over with one exception that matters: the convenience that shortens the
declaration, `sqlrepo.New`, returns a `crud.Repo` and throws away the
`*Blueprint` (`crud/sqlrepo/blueprint.go:252`), and with it `Meta()`
(`:242`) — which `query.Config.Check`, the metamodel and `probe.Full` all need.
Take the short path and the three things that would have validated your
declaration are out of reach.

## Release blockers found here

**This table is the general remit only.** The fourteen module sweeps carry their
own active blockers and a tag has to clear both sets. The three
that are load-bearing for the cases above and are not in this table: a preload
has no row ceiling and nothing can give it one (`query` row 1); a cursor over a
`sql.Null*` column is accepted and returns a page short of rows (`crud` row 1);
a `Save` carrying a tombstone's key resurrects the row (`sqlrepo` row 2).
`sqlrepo.Scope` reaching neither `Save` nor `SaveAll` was the fourth and is
closed (`sqlrepo` row 1): a declared scope now narrows the row a keyed write may
reach and the read-back that follows it ([[D-011]]). The former fifth
item — the source-level COPY bypass — is retained as a closed historical row in
the adapters sweep. The rest are in their own tables, which is where their fixes
live.

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | The organisation in the module path is undecided and a tag freezes it (`docs/roadmaps/Roadmap.md`, item 1) | blocker | Every consumer's import path is decided by `v0.1.0`; after it, changing the name costs a deprecation cycle rather than a `sed`. |
| 2 | The README's flagship error example promises 422 for a body whose codes resolve to 409 (`README.md:83`; `errs/codes.go:68,70`; `port/kind.go:54-59,71-90`; `port/porthttp/errors.go:62`) | blocker | It is on the first screen, it is the single thing the error-subsystem consumer is described as reading, and it is the number a front-end team hard-codes. |
| 3 | `Errors()` configures nothing on a route this library generated, on all **four** transports, and `README.md:1161` presents that call as how a vocabulary and a catalogue are installed (`crud/http/crudnet/middleware.go:77` against `handler.go:123-133`) | blocker | A consumer following the README ships the library's default English on every CRUD route with no signal at all. The tag ships the README. |
| 4 | A stock mount is open in five query dimensions and serialises the whole model — `password_hash` is filterable, sortable, selectable and searchable, and appears in the body (`crud/query/compile.go:70`) | blocker | Disclosure, not memory, and nobody sees it happen. The only lever is a per-resource presenter that hides the column from the body and leaves it a search oracle. |
| 5 | A stock mount is writable by anyone who reaches it: `Mount` publishes POST, PATCH, PUT, DELETE and bulk-delete unless `ReadOnly()` is passed, and nothing notices that the repository carries no policy (`crud/http/crudnet/handler.go:149-179`) | blocker | It is the README quick start's own shape. The gate is the sixth per-resource fact and by far the most expensive one to forget, and forgetting it fails silently in the safe direction — everything works. |
| 6 | `sqlrepo.MaxLimit` defaults to no cap, so a mounted resource honours `?limit=100000000` (`crud/sqlrepo/blueprint.go:53`, `crud/options.go:252`) | blocker | The quick start ships an endpoint whose page size is chosen by whoever sent the request. Fixing it requires the remote-`GetAll` half too, which is why it is a pair. |
| 7 | Application validation and database violations can never appear in one body on a generated route: `port/service.go:158-161` returns before the write, the hook is transport-shaped, and `errs.FromFieldViolations` is called by nothing | serious | `README.md:1186-1189` says merging them "is the point". The most-hit consumer scenario in the framework has a seam on one transport at a time and a headline promise that cannot be reached from it. |
| 8 | Four packages export a `WithScope` that scopes reads and not `DELETE`, and `README.md:960` lists it in a bare option line beside `WithTransform` with no warning (`crud/http/crudnet/options.go:88-97`) | serious | The option's own doc comment says the failure — 404 on GET, 200 on DELETE, same row — and a consumer reaching for the name they saw in the README gets a resource that looks protected. No single module owns a name collision between `security` and four bindings. |
| 9 | A probe wired without `probe.WithScope` under a `security.Gate` is a cross-tenant existence oracle; and a unique index over any other column is the same oracle through a plain 409, with no probe involved and no mitigation anywhere | serious | Two documented features, each correct alone, reopen the isolation the other was bought for — and the documented mitigation closes one of the two doors while reading as though it closed both. |
| 10 | A rule placed in `BeforeSave`/`BeforeUpdate` does not exist on a worker's path, and the pgx example recommends that placement (`crud/http/crudnet/options.go:102,107`; `port/service.go:158`; `_examples/pgx-fiber/main.go:103-111`) | serious | The API enforces an invariant the batch job does not, both call sites read as correct, and nothing in the tree says so. |
| 11 | A 500's cause reaches nobody on `crudnet` and `crudfiber` (`crud/http/crudnet/options.go:174-193`) | serious | The first production incident has no evidence at all, and choosing net/http for costing no dependency also chooses an API with no post-mortem. |
| 12 | Nothing composes an application: the decorator stack, `specs.Executor`, the renderer and the bounds are per-resource with no way to say them once | serious | Every consumer writes the same private helper, and the failure mode of omitting one on resource fourteen is silent in all five cases. |
| 13 | `query.Config.Check` is called by nothing outside its own file — not by an example, not by the README, not by `WithQuery` — so a misspelled allow-list entry closes a field forever and answers every request naming it with the client's own 400 (`crud/query/compile.go:152-204`) | serious | It is the check that exists because the mistake is inert, on the one value H-GENERAL-04 asks consumers to share across twenty models, and `Check` validates it against exactly one of them. |
| 14 | Historical: `SaveAll`/`Delete(ids...)` were unbounded and source-level `BulkInserter`/COPY bypassed the repository | closed (FW-CORE-003) | Budgeted atomic plans now cover SaveAll/Delete; typed `Repo.InsertBatch` provides pgx COPY magic without leaving Gate/faults/decorators, and portable SQL is automatic or explicitly selectable. The old unmarked driver API was removed before release. |
| 15 | Nothing compares the model against the live schema for any column but the key; nothing writes down the expand-contract order a deploy needs; and `catalog.Reloader` is called by nothing, so a running fleet cannot be told about a migration | serious | A renamed column is a detail-free 500 at the worst moment when the check is fifteen lines over data already loaded — but the check has to be directional or it refuses to boot during a normal deploy, and that rule is nowhere. |
| 16 | Field-level write permission cannot be a function of the principal: `Freeze` is a static list on the policy (`crud/decorators/security/policies.go:183-185`) | serious | The security decorator looks like it covers "an admin may set `role`" and does not; the hand-written substitute is privilege escalation when it is wrong. `security`'s own sweep carries no blocker rows, so this is filed here or nowhere. |
| 17 | An old binding against a new library is not detectable (`docs/roadmaps/Roadmap.md`, item 4), and `errs` becomes a twelfth module right after the tag (item 2) | serious | `go get -u` produces exactly that pairing, `retract` is the only lever, and no module asserts a version floor at start-up. |
| 18 | Soft delete has no API above the repository: no route lists tombstones, no verb restores one, and a repository that declares `SoftDelete` can no longer issue a real `DELETE` | serious | It is a headline feature, and the three requirements every product grows in month two — trash, undelete, GDPR erase — all land outside it. The workaround is a second `Define` at a second prefix with its own policy. |
| 19 | Every handler option restates three type parameters, in four modules | sharp edge | It is the most-typed line in every application built on this. The fix is methods on the handler, the names must keep the `With` prefix because `Query`, `List`, `Create`, `Update` and `Delete` are already exported route handlers, and a method on a `Serving` handler must re-run `RefuseServiceOptions` or `[[D-021]]`'s start-up refusal becomes a silent no-op. |
| 20 | Doc drift a consumer reads first: `README.md:1218-1219` says the error subsystem is off by default when the four named `crudsql` constructors install a classifier (`crudsql.go:159-165`); `docs/modules/en/faults.md:92-94` and `crud/decorators/faults/probe.go:51-54` still say a misplaced `faults.Enrich` refuses at start-up, which `[[D-061]]` made false; `docs/ai/usecases/Index.md` gap 20 is stale on two halves it names; and 15 Go files and 17 markdown files cite two deleted roadmap files | sharp edge | A consumer following the guide adds a constraint that does not exist, or skips a feature they already have. Narrower than round 1 claimed: the `Enrich`-innermost *recommendation* is still right for two behavioural reasons, and only the mechanical sentence is false. |
| 21 | `Define("app.users")` cannot name a non-default schema — `Quote` wraps the whole string as one identifier (`crud/dialect.go:70-72`, `:114-116`) and there is no schema setting — and nothing says so | sharp edge | Enterprise schemas are routine and schema-per-tenant is a large minority of multi-tenant SaaS. Either it is a `search_path` and one sentence says so, or it is a blueprint setting; today it is a runtime error from a table that exists. |
| 22 | No benchmark anywhere in the tree, no statement-per-request count, and no written compatibility policy for a v0 tag | sharp edge | All three are questions in the review where the dependency is proposed, and none has an answer a consumer can find. A consumer who sizes a pool from request rate alone exhausts it on the first `?preload=`. |

## Contested

- **H-GENERAL-26 (the replica) was called a duplicate of the `crud` sweep.** Kept,
  because the general-remit half is not `crud`'s: the timing wrapper H-GENERAL-22
  tells a consumer to write is the same wrapper that silently erases
  `ReadSourcer`. Two remedies from two cases undoing each other is what this file
  is for. The `adapters` cross-reference was wrong and is corrected in the case.
- **H-GENERAL-27 (incremental adoption) was called a restatement of `crud`'s.**
  Kept and narrowed: guarantees 1, 2 and 4 link rather than restate, and the case
  exists for `[[UC-012]]`'s two silent scoped-binding failures, which no module
  sweep lists as a blocker and which look exactly like the correct call.
- **H-GENERAL-12 (the cursor) and H-GENERAL-16 (bulk import) were called
  restatements with no general-remit half.** Both kept, and both now carry one:
  the cursor because the token crosses a service boundary through `remote`, where
  two implementations of the same format meet and no test does; bulk import
  because its general-remit guarantee is that acceleration must not create a
  second architecture below the Gate. `sqlrepo` owns typed/budgeted storage and
  `adapters` owns native COPY; `Repo.InsertBatch` now joins those halves safely.
  Round 1's defences — "a tag freezes wire behaviour" and "every application does
  an import" — were true of every defect in the repository and are dropped.
- **H-GENERAL-09 (field-level write permission) was called `security`'s.** It is,
  and it is kept here because `docs/ai/usecases/modules/security/Security.md`
  carries zero blocker-severity rows: moving it without filing it there loses it.
  The case now says that in its own text rather than leaving the reader to work
  it out.
- **The `vvkit` descriptor type-switch was called a violation of [[D-037]].** The
  mechanism is kept and the challenge is now stated in full under "What it must
  not break", including the sentence that if the owner reads D-037 as covering it
  the package cannot be written in Go and the proposal dies. The
  `vvkit.Permissions(perm)` map keyed by model name was **not** kept: that half of
  the challenge had no argument and is removed from the sketch.
- **Round 1's H-GENERAL-17 (the Criteria API as its own case) is gone.** Its
  verdict was "see H-GENERAL-04" and it carried no guarantee of its own; the real
  finding — that the decorated repository is the value an application needs and
  no convenience returns one — is now the reason `vvkit.Repo` is the primitive
  and returns `specs.Repo`. Round 1's H-GENERAL-19 (catalog reload) is folded into
  H-GENERAL-20, where the deploy-order rule it was half of now lives. Every other
  case is renumbered by the two new cases inserted at 05 and 06.

## Edge cases

### E-GENERAL-01 — Two editors patch the same versioned row
**Shape:** concurrency
**Setup:** Two clients read version 3 of one row and both send a partial update.
**What the consumer does:** They add `db:"version,version"` because their API must refuse a stale edit rather than choose a winner.
**What must happen:** One update succeeds and advances the version; the other returns a conflict, never the other editor's row as though it were its own.
**Today:** ✅ handled
**Evidence:** `crud/sqlrepo/version_test.go:34-52` asserts that `Update` predicates on the version it read and advances it; `crud/sqlrepo/version_test.go:58-94` pins the stale result as both `crud.ErrStaleVersion` and `crud.ErrConflict`, on PostgreSQL and MySQL.
**Blast radius:** none

### E-GENERAL-02 — The version column is omitted at 6pm
**Shape:** misuse
**Setup:** A model has ordinary mutable fields but no `version` tag.
**What the consumer does:** They mount it, assuming PATCH has the same protection as the versioned example without knowing that protection is an opt-in declaration.
**What must happen:** The declaration should either make the missing concurrency policy visible at start-up or document that last writer wins at the call site that writes the model.
**Today:** 🟡 partial
**Evidence:** `crud/sqlrepo/version_test.go:167-183` pins that an unversioned model has no version clause at all. `crud/meta.go:29-33` documents the opt-in tag, but no declaration check or generated mount warns that it is absent; no test found for a consumer-facing warning.
**Blast radius:** silent wrong answer

### E-GENERAL-03 — Pointer: replacement is not an optimistic-lock promise
**Owner:** [Sqlrepo.md](../modules/sqlrepo/Sqlrepo.md) owns the versioned `Save` and generated-replace contract. A consumer must not infer from a protected `Update` that full `Save` has the same stale-write refusal; the owner sweep carries the source verdict.

### E-GENERAL-04 — A bulk update races a saved edit
**Shape:** concurrency
**Setup:** A job runs `UpdateAll` while a client holds a previously read version of one affected row.
**What the consumer does:** They expect the client retry to see a conflict rather than overwrite the batch change.
**What must happen:** Every row changed by the bulk operation advances its version.
**Today:** ✅ handled
**Evidence:** `crud/sqlrepo/version_test.go:133-145` asserts that `UpdateAll` adds `"version" = "version" + 1` to its statement.
**Blast radius:** none

### E-GENERAL-05 — Two credentials reach a generated tenant route
**Shape:** seam | adversarial input
**Setup:** A reverse proxy or retry layer leaves two `Authorization` values on a request: one caller belongs to tenant A and the other to tenant B. The application composes `authnet.Middleware`, `security.ScopeAttr("TenantID", "tenant")`, and a generated Crudnet list route.
**What the consumer does:** They expect the door to refuse an ambiguous identity before its tenant claim becomes the repository scope; the route must not let header order choose which tenant's rows are listed.
**What must happen:** A repeated HTTP credential is a 401/400 with no service call. One verified principal may then reach the common context and tenant gate.
**Today:** ❌ wrong or unhandled
**Evidence:** `auth/http/authnet/authnet.go:49-56` passes only `r.Header.Get` into `Guard.Authenticate`; Go's `net/textproto/header.go:30-38` returns the first header value. `auth/guard.go:95-123` authenticates that one credential and writes its principal to the context; `crud/decorators/security/principal.go:128-145` derives `ScopeAttr` from that principal, and `crud/decorators/security/security.go:323-338` applies it to a list. No test exercises this composed route with repeated HTTP credentials.
**Blast radius:** data leak

### E-GENERAL-06 — A manager points back to another manager
**Shape:** degenerate declaration
**Setup:** A hierarchy model has a relation whose target is the model itself.
**What the consumer does:** They declare `Manager` on `Person` and then ask the repository to resolve it.
**What must happen:** The declaration must resolve without a schema-builder deadlock and preserve the target model.
**Today:** ✅ handled
**Evidence:** `crud/relation.go:93-108` resolves relation targets lazily. `crud/relation_test.go:261-274` proves a self-referencing `Manager` resolves to `Person` and is cached on a second lookup.
**Blast radius:** none

### E-GENERAL-07 — One redaction must not mutate a sibling row
**Shape:** seam
**Setup:** A page has two parents that preload the same to-one child, then a presenter redacts one child's field.
**What the consumer does:** They mutate the returned object for one response item.
**What must happen:** Each parent must own its child value; changing one cannot alter another item in the same successful response.
**Today:** ✅ handled
**Evidence:** `crud/preload_edge_test.go:12-33` asserts two parents receive distinct child pointers and that changing one does not change the other. `crud/preload_edge_test.go:54-73` extends that property through a nested preload.
**Blast radius:** none

### E-GENERAL-08 — A broad preload and a narrow preload name the same path
**Shape:** misuse
**Setup:** Application code composes `Preload("Comments")` with `PreloadWhere("Comments", ...)` in either order.
**What the consumer does:** They expect the broad request to remain broad rather than receive a silently narrowed subset.
**What must happen:** The two requests are folded into one query without the narrowed request deleting rows the broad request asked to receive.
**Today:** ✅ handled
**Evidence:** `crud/preload_edge_test.go:75-110` tests both orderings and rejects a generated `WHERE` that keeps the narrowing; `crud/preload_edge_test.go:113-129` separately pins that two narrow requests intersect.
**Blast radius:** none

### E-GENERAL-09 — An attacker puts SQL text in every query-name position
**Shape:** adversarial input
**Setup:** An untrusted query document supplies statement terminators, comments, path traversal and wildcards as filters, sorts, projections, relations and preload clauses.
**What the consumer does:** They send it through the JSON or query-string door of a public mounted resource.
**What must happen:** The request refuses cleanly, produces no usable partial options, and no caller-chosen text becomes SQL.
**Today:** ✅ handled
**Evidence:** `crud/query/hostile_test.go:45-85` covers every name position. `crud/query/fuzz_test.go:19-31,76-105,108-157` checks both wire grammars for no panic, no partial compile on refusal and no caller text in rendered SQL.
**Blast radius:** none

### E-GENERAL-10 — A denied column arrives in another spelling
**Shape:** adversarial input
**Setup:** `published_at` is not allow-listed, and a client asks for `publishedAt`, `PublishedAt`, `published-at` or a padded spelling.
**What the consumer does:** They try every wire spelling in filter, sort, select and search.
**What must happen:** The deny remains a deny; forgiving field resolution must not turn into an allow-list bypass.
**Today:** ✅ handled
**Evidence:** `crud/query/hostile_test.go:209-247` exercises every spelling across all query dimensions and rejects an unlisted relation too. `crud/meta.go:91-106` is the forgiving field lookup that makes this control necessary.
**Blast radius:** none

### E-GENERAL-11 — The last possible page number
**Shape:** boundary
**Setup:** A client asks for a positive page near `math.MaxInt` with a non-zero limit.
**What the consumer does:** They probe whether offset arithmetic wraps back to page one.
**What must happen:** The request must be an empty page past the end, not a mislabeled page one.
**Today:** 🟡 partial
**Evidence:** `crud/options.go:259-270` saturates a would-overflow offset at `math.MaxInt`, which is the correct code path. No test found for the boundary through a mounted transport.
**Blast radius:** confusing error

### E-GENERAL-12 — The request body is exactly the cap, then one byte larger
**Shape:** boundary
**Setup:** A client posts bodies at a resource's byte limit and one byte over it.
**What the consumer does:** They repeat the request on a generated HTTP route.
**What must happen:** The exact-limit body is accepted; the larger body is 413 and reaches neither decoder nor repository.
**Today:** ❓ unverified
**Evidence:** `port/porthttp/body.go:62-85` implements the inclusive one-byte-past check. `crud/http/crudnet/edge_test.go:521-572` pins an over-cap refusal/no repository call and an ordinary under-cap control, but no exact-cap or one-byte-over control was found through Crudnet, Gin, and Fiber. The implementation is promising, not a three-binding boundary verdict.
**Blast radius:** confusing error

### E-GENERAL-13 — A remote peer returns a page one byte too large
**Shape:** boundary
**Setup:** A dependent vv service or proxy answers at the configured response cap and one byte over it.
**What the consumer does:** They call it through `remotehttp.Transport`.
**What must happen:** The exact-limit answer is read, and the larger response is refused before it can grow the consumer process without bound.
**Today:** 🟡 partial
**Evidence:** `remote/remotehttp/transport.go:144-155` reads one byte past the cap and refuses only the oversize response. `remote/remotehttp/transport_test.go:70-84` pins acceptance of an under-cap answer, but no test covers the exact-cap boundary the consumer relies on.
**Blast radius:** confusing error

### E-GENERAL-14 — The caller cancels an outbound read
**Shape:** partial failure
**Setup:** A worker starts a remote read and its job context is cancelled before the peer responds.
**What the consumer does:** They cancel the context they passed to the repository call.
**What must happen:** The outbound request ends with `context.Canceled`; a library backstop must not keep the request alive.
**Today:** ✅ handled
**Evidence:** `remote/remotehttp/transport.go:116-140` creates the request with the caller's context. `remote/remotehttp/transport_test.go:87-100` waits for the server context to end and asserts the caller receives `context.Canceled`.
**Blast radius:** none

### E-GENERAL-15 — The database commits after the client disconnects
**Shape:** partial failure
**Setup:** A write reaches the database, the connection breaks before the client reads the response, and the client retries.
**What the consumer does:** They need to know whether the first create or replace committed before issuing another one.
**What must happen:** The framework must provide an idempotency mechanism or state plainly that the outcome is ambiguous and leave a safe retry protocol to the application.
**Today:** ❓ unverified
**Evidence:** `remote/remotehttp/transport.go:116-160` issues one request and returns the transport result; it has no retry or idempotency branch. No idempotency-key API or end-to-end test found in the mounted request path.
**Blast radius:** data loss

### E-GENERAL-16 — Pointer: a second unique key changes `Save` by driver
**Owner:** [Sqlrepo.md](../modules/sqlrepo/Sqlrepo.md) owns keyed `Save` and dialect-specific upsert behaviour. Its edge cases must decide the PostgreSQL/MySQL second-unique-key conflict; General keeps only this release-level pointer.

### E-GENERAL-17 — Pointer: gRPC numeric IDs require a lossless spelling
**Owner:** [Crudgrpc.md](../modules/crudgrpc/Crudgrpc.md) E-CRUDGRPC-01 owns the `google.protobuf.Struct` numeric-ID and int64-result contract. General does not repeat its source verdict; consumers should use that owner’s string-key rule.

### E-GENERAL-18 — A malformed declaration has no key, two keys or an impossible relation
**Shape:** degenerate declaration
**Setup:** A consumer declares no primary key, a composite key, an unsupported relation shape or an embedded pointer.
**What the consumer does:** They rely on package initialization to catch the mapping before serving traffic.
**What must happen:** Each declaration fails loudly with the field and the reason, not as a zero-value read or a request-time panic.
**Today:** 🟡 partial
**Evidence:** `crud/schema_edge_test.go:42-111` covers non-struct models, no or composite keys, duplicate columns and embedded pointers. `crud/schema_edge_test.go:193-260` covers invalid relation kinds and shapes. `crud/meta.go:222-229` supplies `MustSchemaOf` for a package-level start-up check, but it is an opt-in call; no framework-wide declaration-validation path invokes it for every mounted or defined model before traffic.
**Blast radius:** confusing error

### E-GENERAL-19 — The first row of a batch is invalid
**Shape:** partial failure
**Setup:** An import calls `SaveAll` with a batch containing one row that the database rejects.
**What the consumer does:** They need a truthful answer about whether rows were written and, when faults are enabled, whether the reported violations are complete.
**What must happen:** The API must document atomicity by dialect and mark an enrichment it cannot finish as partial; it must never imply that every row was considered when it was not.
**Today:** 🟡 partial — atomicity is covered; enrichment completeness remains conditional
**Evidence:** `SaveAll` preflights its complete chunk plan and executes more than
one statement through one transaction. `TestSaveAllRollsEveryChunkBackWhenALaterChunkFails`
pins the storage seam and
`TestSaveAllChunksRollBackAsOneWriteAgainstEveryEngine` proves the rollback on
every live engine. The remaining partial half is fault probing:
`crud/decorators/faults/probe.go` can mark a batch request partial when it cannot
construct or inspect the complete row set, and probing remains opt-in per
operation.
**Blast radius:** confusing error

### E-GENERAL-20 — Empty and over-cap bulk deletion
**Shape:** boundary
**Setup:** A UI retries an empty selection, then submits one more id than the bulk cap.
**What the consumer does:** They call the generated bulk-delete route rather than hand-writing an empty-set branch.
**What must happen:** An empty set answers deleted 0 without touching the repository; an oversize set refuses before a destructive call. `port.Rules.BulkCap` is the shared policy owner, but each binding needs its own conformance proof.
**Today:** 🟡 partial
**Evidence:** `port/rules.go:43-65` makes `Rules.BulkCap` the non-zero shared owner. `crud/http/crudnet/handler.go:387-401` checks that owner before the service call. `crud/http/crudnet/handler_test.go:460-473` pins that an empty set does not call the repository, `crud/http/crudnet/edge_test.go:207-217` repeats that assertion for omitted and null ids on the wire path, and `crud/http/crudnet/options_test.go:276-297` proves the exact cap calls the repository while one extra id refuses before it. This evidence proves the Crudnet journey only; General has no conformance evidence that Gin or Fiber read the cap.
**Blast radius:** none

## Edge verdict

The cross-stack edge is an ambiguous HTTP identity: `authnet` reads the first credential, so a proxy can choose which authenticated principal becomes a generated tenant route’s scope. The canonical Sqlrepo and Crudgrpc sweeps carry the separate `Save` and numeric-ID integrity verdicts; General points to them rather than competing with their source ownership. Hostile query text and query alias bypasses are closed; request-body cap code has the right one-byte-past shape but lacks an exact/plus-one binding-triplet verdict. `port.Rules.BulkCap` is the one bulk-cap policy owner, and Crudnet proves its empty/exact/over-cap journey; Gin and Fiber conformance remains unverified here. Schema resolution rejects malformed mappings when invoked, but no universal start-up declaration pass invokes it. Remote response code has the right one-byte-over implementation but lacks the exact-cap test, while ambiguous write outcomes after a disconnect and failed-batch fault-enrichment completeness remain the unverified write edges; cross-dialect batch rollback itself is covered.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `authnet` gives the first repeated HTTP `Authorization` value to the authenticator, whose principal becomes the tenant scope (`auth/http/authnet/authnet.go:49-56`; `auth/guard.go:95-123`; `crud/decorators/security/security.go:323-338`) | serious | A proxy or hostile client can make header order choose an otherwise valid caller’s tenant view. The request succeeds with no record that the door was ambiguous. |

## Contested

The round-1 DX concern about `vvkit` is **out of this edge pass**, not a new API proposal. The happy-half DX section already states its explicit, conditional challenge to [[D-037]]: if a generic descriptor type-switch is read as dependency resolution, the package cannot be written. No compatibility plan can be chosen by a readiness sweep. [[D-033]] and [[D-036]] also prohibit a root package that imports a transport dependency, so any future kit must return transport-neutral values; [[D-060]] already assigns the bulk cap to `port.Rules.BulkCap`. This round accepts those boundaries and adds no competing cap or generic-service design.

Page-cap authority and migration are not a General-owned safe-mount proposal. The happy-half `sqlrepo.MaxLimit` default is merely an input/alternative for the Query sweep's single pending [[D-060]] amendment; Query alone selects the authority and migration, including clamp/refusal and remote-`GetAll` consequences, rather than adding a third configuration surface here (`docs/ai/usecases/modules/query/Query.md:1209-1213`). The `house.With(...)` occurrence in the happy-half sketch is explicitly illustrative and proposed, not an existing API; the immediately following text records that it “does not exist today” (`docs/ai/usecases/general/General.md:575-579`).

Authhttp owns repeated HTTP credential detection and refusal in E-AUTHHTTP-13 (`docs/ai/usecases/modules/authhttp/Authhttp.md:714-730`). General retains E-GENERAL-05 only for the generated-route and tenant-scope consequence after that transport choice; Release-readiness must merge the two records as one underlying fix.

Port owns the renderer-install seam in its release-blocker row 1 (`docs/ai/usecases/modules/port/Port.md:1203-1208`). General's happy blocker 3 remains the README-wide, all-transport consumer blast radius, not a competing implementation owner; Release-readiness must count the repair once.
