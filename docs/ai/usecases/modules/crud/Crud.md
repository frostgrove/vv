# crud — the vocabulary a repository, a filter, a write and a page are written in

**Covers:** `github.com/frostgrove/vv/crud`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — four things a client can see are wrong and none of them raise an error: a paged read over a nullable sort column hands back a cursor its own next request refuses, and over a `sql.Null[T]` column hands back one that is accepted and returns a page short of rows; `Save` over a key the client chose overwrites the row that was there, with no version check and no way to ask for an insert; a dashboard summary is cut to twenty groups; and eleven options handed to a preload are accepted and dropped. Separately and cheaply, the consumer reference names three `Opt` accessors that do not compile and omits the two that matter most. The edge pass adds an unbounded manual ID filter, a first-use schema-cache identity split, and unpinned cursor and transaction-cleanup paths.

## What a consumer is actually trying to do

Someone has a table and a product. They want the eight things every resource
needs — list it, filter it, sort it, page it, fetch one, create one, patch one
field, delete it — without writing them eight times for twenty resources. They
arrive here because they were told the struct they already have is enough.

The first hour is spent on the model: does the column mapping match the table
they already migrated, and will they find out on Tuesday or in production? The
first day is the two ends of the write path — a create endpoint whose response
describes the row the database actually stored, and a partial update that can
tell "the client left this field out" from "the client wants this field
cleared". The first week is filters: a status, a date range, a tenant, a name
from a related table, and a page a frontend can render.

Then two people edit the same order at the same time, and the loser has to be
told which kind of loser they are — the row is gone, or the row moved on and
they should read it again. Nobody wants to write that retry loop twice.

By the second week the questions change shape. They already have a database
connection, maybe opened by an ORM, and they want this to run on it rather than
beside it — including inside a transaction somebody else opened, and without a
write landing in the analytics database because the code said "in a transaction"
and meant "in *that* transaction". They want reads on the replica, but not the
read that decides a write. They want their tracing on every statement, and a
rule of their own — an audit line, a soft-delete, a permission check — sitting
above the repository where nothing can go round it. Somewhere in there a nightly
job appears that writes twenty thousand rows and deletes ten thousand, and the
verbs it calls are the same ones the handler calls.

By the second month they want the thing the vocabulary does not have a word for:
a JSON containment test, a full-text match, a summary ranked by what it summed.
The question then is whether reaching further means one more argument or a drop
into hand-written SQL that leaves every narrowing behind.

Underneath all of it is one expectation they will not state: that a filter they
did not write cannot be removed by a caller who did. A tenant scope, a
soft-delete filter, a permission narrowing. If any option can widen a query, the
whole layer is decoration rather than enforcement.

## Happy cases

### H-CRUD-01 — The struct they already have becomes the mapping
**Who:** a backend engineer adding the first resource to a service with an existing schema
**Wants:** the column names, the key and the nullable columns to come off the struct, and any disagreement to be a start-up failure
**Story:** They paste their existing struct, tag the key, and declare a repository at package level. The service either starts or does not. They never write a mapping file, and they never look up "how do I tell it the column is called `created_at`".
**Must hold:**
1. An untagged exported field is still mapped, with the column derived in snake_case.
2. An embedded struct is flattened into its columns; a `time.Time` or anything with a `Valuer` is one column, not a struct to walk into.
3. A column declaration that cannot work — no key, a duplicate column, an unknown tag option, a tagged unexported field, a `version` column that is not an integer — fails when the process starts, not on the request that first touches it.
4. A `rel` declaration that cannot work gets the same treatment: a relation whose join field does not exist is a start-up failure, not a 500 on the first request that crosses it.
5. A `db:"-"` field, a `func` or `sync.Mutex` field, and an untagged struct-shaped field with no `Valuer` are skipped. Anything carrying a `db` tag either becomes a column or fails at start-up — there is no third outcome.
6. The derived names are readable from Go, so a test can diff them against the migration.

**Today:** 🟡 partial — (4) fails by design, (5) fails twice
**Evidence:** `crud/meta.go:340` maps every exported field and derives the column at `:398`; embedded flattening at `:347`, one-column structs at `:482`. Eighteen named refusals in `crud/meta.go`: `:234`, `:249`, `:260`, `:303`, `:349`, `:353`, `:366`, `:384`, `:391`, `:419`, `:423`, `:426`, `:430`, and five more reached through `deny` at `:321` that only a `version` column can trip. `Define` refuses through three more paths that are not in that count: `parseRelation` (`crud/relation.go:235`, `:265`, `:269`, `:272`, `:286`), `CheckID` (`crud/access.go:119`), and `PlanFor` on the update DTO (`crud/update.go:109`, `:114`, `:116`, `:118`, `:120`, `:140`). Pinned by `crud/schema_edge_test.go:47`, `:109`, `:134`, `:151`, `crud/meta_test.go:70`, `crud/version_test.go:37`. `Schema.Columns` at `crud/meta.go:179` is what [[UC-010]] guarantee 6's diff test reads.

(4) fails, and it is a consequence of a deliberate design rather than an oversight: **relations are never resolved at `Define` time.** `TryDefine` (`crud/sqlrepo/blueprint.go:158-196`) calls `NewMeta`, `CheckID`, `PlanFor`, `resolveSoftDelete` and `resolveRelationScopes`, and nothing calls `Relation.Resolve()`. Resolution is lazy so models may reference each other in a cycle (`crud/relation.go:94`, `:300-302`), so a `belongs_to Author` whose `AuthorID` field was renamed starts the process and answers `"relation references unknown field AuthorID"` on the first request that filters or preloads through it (`crud/relation.go:119-120`). That is the one hole in this case's own start-up story, and the DX section's example depends on it.

Two ways a `db` tag is dropped in silence, both at `crud/meta.go:375`, which decides on the `rel` tag alone and never consults the `db` tag:
- A **struct-shaped field carrying an explicit `db:"…"`**. `Prefs Preferences` tagged `db:"prefs"`, with no `Valuer`, becomes neither a column nor an edge. No error, no column, and the field reads back zero forever.
- An **embedded struct carrying any `db` tag**. `crud/meta.go:347` flattens an embedded struct only when it has no tag, so an embedded `Base` tagged `db:"base"` falls through to the same skip and the whole mixin's columns disappear at once. The shape is a hand-written mixin whose author believed `db:"base"` was a column prefix; an adopted ent or gorm entity carries `gorm:`/`json:`/ent tags and no `db` tag, so it still takes the flattening path — which is exactly why [[UC-010]] guarantee 4 can promise flattening at all. `crud/schema_edge_test.go:98` tests an embedded *pointer* and `:93` tests shadowing; a tagged embedded struct is tested nowhere.

`crud/meta.go:361` refuses the same class of typo on an unexported field, and the comment says why: silently dropping the column would show up as a zero in the row rather than as an error here. `crud/relation_test.go:279` pins only the *untagged* case, which is the one that should be silent.
**If not ready:** They discover it when a value they wrote comes back empty. The fix has to be scoped so it cannot fire on an adopted ORM model: [[UC-010]] guarantee 3 promises that struct-shaped fields carrying no relation tag are skipped, "in particular an ORM's own eager-loading holder". So refuse only when the `db` tag *names a column* (`db:"prefs"`, `db:"base"`), never on an option-only tag (`db:",something"`) and never on an untagged field — and add that exception to [[UC-010]] guarantee 3 in the same change. (4) is a bigger question: a `Define`-time pass resolving every relation would have to tolerate the cycle the laziness exists for, so the honest cheap version is a `Blueprint.Verify()` a consumer calls from `TestMain` once every model in the process is declared.

### H-CRUD-02 — A join table with a real composite key becomes a resource
**Who:** the same engineer, on day three, reaching `memberships(user_id, org_id, role)`
**Wants:** to list, filter and patch memberships the way they list users
**Story:** The membership row is not decoration — it carries a role, an invited-at, a status. They want `/memberships?org_id=…` for the same three lines every other resource cost. The table's key is the pair.
**Must hold:**
1. A model whose identity is two columns is declarable.
2. Failing that, the refusal names the limitation where a consumer models, before they design around it.

**Today:** ❌ missing on (1), 🟡 on (2)
**Evidence:** `crud/meta.go:430` — a second `pk` field is `"composite primary keys are not supported"`, and `Schema` holds one `PK` (`crud/meta.go:94`). A deliberate boundary, stated in [[D-021]] and revisited in [[D-042]]. One consumer-facing reference does carry it — `docs/modules/en/probe.md:197-199`, in almost these words — but that is the probe's page, reached after the model is already written. Neither `docs/modules/en/crud.md`'s model section nor the README's model section nor its Sharp edges list mentions it, and those are the two pages open while somebody is deciding what the join table's key is.
**If not ready:** They add a surrogate `id` to the join table — usually the right answer anyway — or they keep that one resource on hand-written SQL, which is exactly the query that then runs outside the tenant scope. Closing the documentation half costs one row in the tag table and one line in Sharp edges. Closing the capability half is a seam change and belongs to a phase, not to a release.

### H-CRUD-03 — `POST /articles`, and the row the database finishes
**Who:** the engineer writing the create endpoint, on the first afternoon
**Wants:** to hand over a model and get back what was actually stored
**Story:** They decode the body onto the model, call save, and serialise the result. The id comes from the sequence, the timestamp from the column default, and the tenant id is written once and never again. A duplicate email comes back as something they can turn into a 409.
**Must hold:**
1. A model with an unset generated key is inserted, and the key is filled in on the value they passed.
2. A column the database computes comes back on the model without a second read written by hand.
3. A column tagged immutable is written on insert and can never be written by an update.
4. A duplicate key or a foreign key pointing nowhere is a failure the handler can branch on, not a 500.
5. A column the migration gave a `DEFAULT` gets that default when the application does not set it.
6. Creating a row that already exists is distinguishable from updating it.

**Today:** 🟡 partial — (5) and (6) fail
**Evidence:** (1) `crud/sqlrepo/repository.go:617` branches on `HasID`; the generated-key path uses `Schema.InsertGen`, which omits the key (`crud/meta.go:291`), and `insert` writes it back from `RETURNING` or `LastInsertID` (`:641`, `:656`). (2) `generated` columns are excluded from every insert list (`crud/meta.go:285`) and `insert` re-reads when the dialect has no `RETURNING` — the comment at `crud/sqlrepo/repository.go:661` names what skipping it cost: a handler serialised a different document on MySQL than on PostgreSQL. (3) `crud/meta.go:297` keeps `immutable` and `version` out of `Schema.Update`, so no update statement can name them. (4) is not `crud`'s: `crud` owns the `ErrConflict` sentinel (`crud/errors.go:31`) and the adapters classify the driver's SQLSTATE into it (`crud/adapter/crudsql/conflict_test.go:74`).

(5) fails, and it is already written down: vv names **every** mapped non-generated column in the INSERT, so `active boolean NOT NULL DEFAULT true` stores `false` on every row the application creates. `README.md:1545-1551` states it. It is absent from `docs/modules/en/crud.md`, which is the page open while the model is being written.

(6) fails, and this is the one worth stopping for. `Save` is `INSERT … ON CONFLICT DO UPDATE` whenever the key is set (`crud/sqlrepo/repository.go:623`), and the conflict clause assigns **every** column of `Schema.Update` from `EXCLUDED` (`crud/dialect.go:80`). A model whose key the client supplies — a uuid, a natural key, an imported id — always has its key set, so a "create" that collides silently overwrites the row that was there. There is no `Insert` on the seam and no option that asks for one. **[[D-011]] forbids adding one; the challenge is stated plainly in *What it must not break* rather than smuggled in here.**
**If not ready:** For (5), the remedy the references give is wrong in one of them and incomplete in the other. There is no `BeforeSave` hook at this layer — `grep -rn "BeforeSave" --include="*.go" .` finds it only as a per-request option on the four transports (`crud/http/crudnet/options.go:102` and its three siblings) — so a service method, a background job or a `SaveAll` has no such seam. What exists is tagging the column `generated`, which gives up ever writing it from Go, or a `crud.Middleware` that fills the field before delegating. The same wrong remedy is Sqlrepo blocker 17 against `docs/modules/en/sqlrepo.md:283`; it is one edit in two files. For (6), a create endpoint that must not overwrite is written as a pre-flight `Exists` — check-then-act, so it is wrong under concurrency — or by giving up on `Save` for that resource.

### H-CRUD-04 — A PATCH that can clear a field and a PATCH that leaves it alone
**Who:** the engineer wiring the edit form for a profile with an optional "age"
**Wants:** three outcomes from one DTO field, in Go and on the wire
**Story:** The form posts only what changed. Sometimes the user clears the field. They hold one type in the DTO, decode the body onto it, and hand it to the repository. They never write `map[string]any`, and they never lose a field because the form omitted it.
**Must hold:**
1. Absent, explicit null and a value are three distinguishable states, and there is a way to ask which one a field is in.
2. An absent key decodes to "absent" rather than to the zero value.
3. The same type works as a model field for a nullable column: it scans NULL and binds NULL.
4. Marshalling round-trips: a set value marshals bare, an absent one disappears — and a model this service serialised, posted back to it as a PATCH, does not clear the fields nobody touched.
5. The state survives a second decode over a value that already held something.
6. A DTO field's *type* decides whether omission means anything, and the reference says which is which — so a hand-written DTO cannot make a PATCH clear a field nobody touched.

**Today:** 🟡 partial — the type is right, the reference that documents it is wrong in both directions, and (4)'s round trip has a trap
**Evidence:** `crud/opt.go:32` and the three constructors at `:38`–`:48`; `UnmarshalJSON` at `:164` (an absent key never reaches it, which is what makes state 1 free); `Scan` at `:131` and `Value` at `:107`; `IsZero` at `:93` for `omitzero`. Pinned by `crud/opt_edge_test.go:19`, `:50`, `:81`, `:264`, `:276`, `:326`, and by [[UC-003]] end to end. [[D-002]] is the decision.

The DTO's own start-up net is real, and it is what makes (6)'s remaining hole sharp rather than general: `PlanFor` refuses at `Define` time a DTO field naming no model field (`crud/update.go:109`), naming the primary key (`:114`), a `generated` column (`:116`), an `immutable` one (`:118`), the `version` column (`:120`), or whose element type does not match the model's (`:140`). Six refusals. What it cannot catch is the seventh: `crud/update.go:13` — `planPlain planKind = iota // T — always applied` — so a hand-written DTO with `Name string` writes an empty name on every PATCH that omits the field, and a nil `[]byte` in a `T` field writes NULL. `README.md:1566-1567` lists it under Sharp edges; the generator never emits a plain `T` (`_examples/example/blog/vv_gen.go:19-26` is all `*T` and `Opt[T]`), so the trap is set only for a DTO somebody wrote by hand — which is what a consumer does first, before they find `cmd/vv`.

(4)'s trap is on the same type and a consumer meets it earlier, because their own client produces it. `MarshalJSON` returns `null` for **both** undefined and null (`crud/opt.go:152-159`), and `omitempty` does nothing to a struct type — so `json:"age,omitempty"`, which is what tag muscle memory writes, emits `"age":null`, which decodes back as null and writes SQL NULL. Only `omitzero` drops it. Pinned at `crud/opt_edge_test.go:81` and surfaced nowhere a consumer reads.

The reference is wrong in both directions. `docs/modules/en/crud.md:84`, `:85`, `:87` and the identical lines in `docs/modules/ru/crud.md` document `o.Defined()`, `o.Valid()` and `o.OrZero()`. The methods are `IsDefined()`, `IsSet()` and `OrElse(def)` (`crud/opt.go:56`, `:62`, `:76`); there is no `OrZero`. Three of the five accessors that block lists do not compile. Absent from the block are `IsNull()` (`crud/opt.go:59`) and `MustGet()` (`:68`) — and `IsNull` is the whole reason the type has three states, so must-hold 1 depends on an accessor the reference does not mention. `grep -n "IsNull" docs/modules/en/crud.md` finds one line, `:129`, which is the *predicate* `crud.IsNull` and sends a grepper down the wrong path.
**If not ready:** They read the compile error, open the source and find the real names — thirty seconds, and a dent in the trust the rest of the reference is asking for. Fix both files in both directions, and put the plain-`T` rule and the `omitzero`-not-`omitempty` rule in the reference beside `Opt` rather than only in the README's Sharp edges.

### H-CRUD-05 — Two people editing the same order
**Who:** the engineer on an admin screen two support agents have open at once
**Wants:** the second write to lose, and to be told which kind of loss it was
**Story:** They tag an integer column and stop thinking about it. Later one agent saves over a row the other already changed. The API answers 409 and the client reloads; a row that was deleted meanwhile answers 404 and the client stops.
**Must hold:**
1. Declaring the lock is a tag, and a declaration that cannot be a lock fails at start-up.
2. The counter advances on every write that touches the row, including the filtered one that has no single row to check.
3. A write built on a stale copy matches nothing rather than overwriting.
4. "The row moved on" and "the row is gone" are two different failures, and the caller can branch on them.
5. The caller cannot set the counter themselves, and no write path can wind it backwards.
6. Every write path that can overwrite a row is covered, or the ones that are not say so.

**Today:** 🟡 partial — (6) fails, and (2) has a hole a caller will not connect to this
**Evidence:** (1) `crud/meta.go:319` refuses five ways a `version` cannot be a lock — two versions, the key, `immutable`, `generated`, a non-integer — and the comment names what each one fails as at run time. (2) and (3) are `Update`: `crud/sqlrepo/repository.go:749` adds `version = version + 1` to the SET and `:751` puts the value it read into the WHERE, under the comment at `:740` that names both halves. `UpdateAll` advances every row it writes without checking one, which is right and is why: a stale `Update` somebody is holding would otherwise sail past the bulk change and undo it (`crud/sqlrepo/repository.go:853`, pinned at `crud/sqlrepo/version_test.go:136`). (4) `missedRow` at `crud/sqlrepo/repository.go:817` asks whether the row is still there and answers `ErrStaleVersion` or `ErrNotFound`. (5) `crud/meta.go:297` keeps the column out of `Schema.Update`, and `crud/sqlrepo/version_test.go:151` asserts the upsert's conflict clause leaves it alone.

(6): **`Save` cannot check the lock, and nothing says so where a consumer looks.** The test comment states it plainly — "Save is an upsert — one statement, no WHERE clause for a version to live in — so it cannot check the lock" (`crud/sqlrepo/version_test.go:147-150`), and [[D-011]] gives the same reason. What it does guarantee is only that it will not wind the counter *back*. So a consumer who tagged `version` and reasonably believes concurrent writes are guarded gets last-write-wins across every updatable column the moment the write goes through `Save` rather than `Update` — which is every `PUT`, and every create over a client-supplied key. This is H-CRUD-03 (6) seen from the concurrency side: one defect, three symptoms, blocker 3.

The hole in (2): an `Update` whose DTO diffs to nothing returns the loaded row with no statement at all (`crud/sqlrepo/repository.go:729`). That is right for [[UC-003]] guarantee 5, and it means the counter does not move and a database `updated_at` trigger never fires. An audit trail notices; nobody connects it to this.
**If not ready:** The retry loop is the caller's and is fine. What a consumer writes by hand today is the guard `Save` does not have: reload, compare, and refuse — check-then-act, so it is wrong under exactly the concurrency it was written for. Either `Save` grows a version-aware conflict clause on the dialects that can express one, or the reference says in one line that the lock covers `Update` and `UpdateAll` and not `Save`.

### H-CRUD-06 — Build the list query out of what the request asked for, and reuse the pieces
**Who:** the engineer writing a search endpoint by hand, because this one is not a plain CRUD route
**Wants:** to accumulate filters conditionally and hand the same filter to a background job later
**Story:** The handler has a filter struct. Status set? Add a term. Author name set? Add a term across the relation. They pass the accumulated options to the repository. Next month the nightly export needs the same filter, so they lift it into a function and both call sites use it.
**Must hold:**
1. A bundle of options can be built in a slice, appended to conditionally, held in a variable and replayed into another read.
2. A helper with nothing to add is a no-op, and needs no `if` around it at the call site.
3. Two *filters* added by two different pieces of code are ANDed. Nothing in the shipped option vocabulary removes or weakens a predicate another layer added.
4. Where an option does replace rather than add — the sort, the projection — that is a different call from the one that adds, so a default and a user's choice do not silently stack.
5. An option a call cannot honour is refused, not accepted and dropped.

**Today:** 🟡 partial — (1)-(4) hold; (5) fails on six of the eleven seam verbs
**Evidence:** `Option` is `func(*Options)` (`crud/options.go:57`) and every read is variadic, so a `[]crud.Option` is the natural shape. `Build` skips a nil option (`:68`) and `Where` skips a nil predicate (`:88`), so a conditional term needs no guard. `Where` appends and there is no option in the package that unsets a predicate — [[D-004]], pinned by `TestWhereAccumulates` at `crud/options_test.go:60`. (4) is the honest half of (3): `OrderBy` appends and `SortBy` replaces (`:146`, `:151`, pinned at `crud/options_test.go:81`), and `Select` appends while `SelectAll` sets `o.Fields = nil` (`:157`, `:165`). Both replacements are deliberate and neither can widen a *row set* — `SelectAll` widens a projection, which is why a row-level check calls it: reading a column the client did not select would compare against a zero value and believe it (`crud/options.go:161`).

(1) `With(*Options)` replays a stored shape (`:207`) and carries the relation narrowings with it, for the reason the comment at `:196` gives — a `With` that dropped them once produced a page narrowed by the gate beside a `Total` counted over rows the gate hides. Pinned by `TestWithReplaysAStoredShape` at `crud/options_test.go:99`, and the narrowing half by `TestEveryStatementAGatedCallIssuesCarriesTheNarrowing` at `crud/decorators/security/relscope_test.go:367`.

(3) is worth stating precisely, because the stronger version is false. `Options` is an exported struct with an exported `Filter []Predicate` — deliberately, so decorators can inspect it (`crud/options.go:7-10`) — and `Option` is a function over `*Options` (`:57`). Any package can therefore write an option that clears the filter, and the gate applies the caller's options *after* its own scope (`crud/decorators/security/security.go:237`, `:633`; options run left to right, `crud/options.go:67-73`), so such an option would run last and win. What holds is that no such option exists in the vocabulary and [[D-004]] forbids adding one. That is a rule for authors, not a property the type enforces, and a consumer auditing this should know which of the two they have.

(5) is the gap, and it is a property of the vocabulary rather than of any one method: one option list applied to eleven seam verbs with eleven different honoured subsets, and nothing anywhere states them.

| verb | silently drops |
|---|---|
| `Get`, `GetByID` | `Agg` |
| `GetAll` | `Agg` — and see H-CRUD-07 (7) for what `Unpaged()` does here |
| `Count` | `Sort`, `Preloads`, paging, `After`/`Before`, `ForUpdate` |
| `Exists` | everything but `Filter`, `RelScopes` and `Primary` |
| `UpdateAll`, `DeleteAll` | `Limit`, `Page`, `Offset`, `Sort`, `Preloads`, `Fields` ([[D-026]], status open) |
| `Aggregate` | `Fields`, `Preloads`, `Distinct`, `ForUpdate`, `After`, `Before`, `Primary` |
| a `PreloadWhere` option list | eleven, listed in H-CRUD-10 |
| `Save`, `SaveAll`, `Delete` | take no options at all ([[D-011]]) |

`UpdateAll` at `crud/sqlrepo/repository.go:834-871` and `DeleteAll` at `:903-918` never read `o.Limit`; `Aggregate` at `:1028-1055` reads only `o.Agg`, `o.Sort`, `o.NoSort` and the paging. "Delete the ten oldest" is therefore a filtered write that silently does more than it was asked, and it is the same shape as the preload drops.

Two edges worth knowing before storing a shape: a bundle is a `*Options`, so reuse is `crud.With(crud.Build(…))` rather than one value; and `With` deliberately does not replay `After`/`Before`/`Agg` (`:203`). "Stored" in the persistent sense — a saved search, a scheduled report — is `crud.MarshalPredicate` (`crud/document.go:31`), which covers the predicate only and refuses `Raw`, `EqField` and `False` by name ([[D-054]]): the sort, the preloads and the projection do not survive that round trip.
**If not ready:** For (5) there is nothing to write by hand — a consumer cannot discover the subset from any document, so they find out from a row count. The cheap close is symmetry with the one refusal that already exists (a preload refuses paging, `crud/preload.go:193`): every verb refuses the options it cannot honour, at `Build` time or at the top of the method. The expensive close is honouring them.

### H-CRUD-07 — A page a frontend can render, and the number on it
**Who:** the engineer wiring an admin table with a pager
**Wants:** items, total, page count and next/prev flags, and the items as response DTOs rather than models
**Story:** They call the list with a page and a limit, map each row to the shape the API publishes, and return it. They do not compute `totalPages` by hand.
**Must hold:**
1. The response carries everything a pager needs and is JSON-ready as it stands.
2. An empty page marshals as `[]`, not `null`.
3. Converting the items to another type keeps the arithmetic.
4. A page number large enough to overflow an offset is not silently turned back into page one.
5. Page 2 neither repeats nor skips a row from page 1 when the sort column ties.
6. A number the response calls a total is a total, or the response says it is not.
7. A client cannot ask for the whole table — and a caller who *does* want the whole table gets it, or is told they did not.

**Today:** 🟡 partial — (6) and (7) fail
**Evidence:** (1)-(4) hold. `crud/page.go:5` is the shape; `NewPaginatedResponse` at `:23` derives `TotalPages`, `HasNext`, `HasPrev` and normalises a nil slice to `[]T{}` (pinned by `crud/page_test.go:10`, `:63`). `MapPage` at `:48`, pinned at `crud/page_test.go:76` and `:91`. The offset saturates rather than wrapping (`crud/options.go:266`), and the comment names the bug it came from: a wrapped offset was dropped as non-positive and the caller got page one labelled page 9223372036854775807.

(5) holds and is a guarantee nothing else in this tree claims: `sortOf` appends a primary-key tiebreaker to every paged sort that does not already end in the key (`crud/sqlrepo/repository.go:540-557`), and `stableSort` defaults true (`crud/sqlrepo/blueprint.go:175`). `ORDER BY created_at` over rows sharing a timestamp is the first pager bug anyone hits and it does not happen here. The opt-out costs more than its name says: `sqlrepo.UnstablePagination()` (`crud/sqlrepo/blueprint.go:63`) also removes the ability to page by cursor, because `cursorWhere` requires the key in the sort (`crud/sqlrepo/repository.go:400`, and the doc comment at `crud/cursor.go:19-22` says so) — so a repository declared that way for a plan shape stops emitting `nextCursor`, with no error. Same shape as blocker 10.

(6): `SkipTotal()` makes `Total` the length of the page and `TotalPages` zero (`crud/options.go:184`, `crud/sqlrepo/repository.go:194`). That is deliberate and right. What is not obvious is that **`After` and `Before` set the same flag implicitly** (`crud/options.go:121`, `:131`), so a cursor-paged feed *always* answers `"total": 20, "totalPages": 0` over three million rows. A frontend rendering "page 1 of N" off that field prints "of 0" on every infinite-scroll endpoint, and nothing in the field name or the JSON says which mode produced it.

(7) fails in both directions, and the second is created by the fix for the first.
- **The cap is off by default.** `Options.Resolved` clamps `Unpaged` down to `maxLimit` (`crud/options.go:241`) and the doc comment says why — a flag arriving from the wire must not talk a repository out of its own cap. But `maxLimit` comes from `sqlrepo.MaxLimit`, whose own comment reads "Zero disables the cap" (`crud/sqlrepo/blueprint.go:54`), and `Define` defaults only `defaultLimit` (`:175`). On a stock `Define`, `crud.Unpaged()` returns the whole table and `crud.Limit(1000000)` is honoured verbatim. [[D-060]] says this in as many words. What is armed is one door up — `query.Config.AllowUnpaged` is closed by default — which is a different module.
- **Once the cap is on, `GetAll(ctx, crud.Unpaged())` returns exactly `MaxLimit` rows while `GetAll(ctx)` returns every row.** The fast path is entered only when `Limit`, `Page` and `Offset` are all zero *and* `Unpaged` is false (`crud/sqlrepo/repository.go:271-284`); adding the most emphatic way a caller can say "yes, all of them" routes through `Resolved` and truncates, with no flag and no error. The doc comment promising "GetAll's contract is every matching row" sits four lines above it, and the only test pinning that contract uses the no-options call. This reaches past a short list: `security.gate` fetches the rows it will inspect through `g.Core.GetAll(ctx, g.whole(true, scoped)...)` and `whole` adds only `SelectAll()` (`crud/decorators/security/security.go:247-253`, `:703`, `:728`), so a caller who passes `Unpaged()` into a gated `DeleteAll` has `Inspect` see `MaxLimit` rows and the `DELETE` take every match. [[D-026]] is open over the `Limit` shape of exactly this and does not list the `Unpaged` one.

Pinned: the clamp itself by `TestResolved` rows at `crud/options_test.go:164-165` and `crud/edge_test.go:447-448`, with the control that makes the point at `crud/options_test.go:151` — "no maximum means no clamp".
**If not ready:** For (6) a consumer either ignores `Total` on cursor endpoints or calls `Count` separately, and finds out from a screenshot. For (7) they pass `sqlrepo.MaxLimit(…)` at every `Define`, once they know to, and then must never write `Unpaged()` against `GetAll`. Whether `MaxLimit` should default non-zero is [[D-060]]'s question, answered there for the wire and not for the in-process caller; the `GetAll` half is a two-line fix — treat `Unpaged` as the fast path rather than as paging — and needs a test beside `TestGetAllIsNotCappedByMaxLimit`.

### H-CRUD-08 — Page a feed that is being written to while it is read
**Who:** the engineer building an infinite-scroll feed, or the one writing an export job that must not skip rows
**Wants:** a position, not an offset
**Story:** They sort newest-first, take twenty, and hand the page's own token back for the next call. Somebody publishes a post in between; the reader neither sees a row twice nor misses one.
**Must hold:**
1. Every paged read hands back the edges of its own page.
2. A token handed back works with the sort it was made for, and is refused under any other.
3. A token the server minted is a token the server accepts.
4. A token the server accepts returns every row it should.
5. When a token cannot be minted, the caller can tell — the API does not go quiet.
6. It works over the sorts a feed actually uses, including "newest first" on a column that is allowed to be null.

**Today:** ❌ broken on (3), (4) and (6); 🟡 on (5)
**Evidence:** (1) and (2) hold and are the well-made half: the token carries its own field names (`crud/cursor.go:27-30`) and `decodeCursor` refuses a mismatch before anything else runs (`:69`, `:74`), pinned at `test/integration/cursor_test.go:238`.

The mint side (`crud/sqlrepo/repository.go:238` `setCursors`) checks only that the sort resolves and contains the primary key. The consume side (`crud/cursor.go:124`) refuses any sort column that is `Opt[T]` or a pointer, because `NULL` never compares and a boundary on a nullable column would silently drop every row that has one. Nothing connects the two, and the disagreement splits two ways:
- **(3)** A model with `PublishedAt crud.Opt[time.Time]` sorted `Desc("PublishedAt")` gets a page carrying `nextCursor`, and sending that token back is `SchemaError{"a cursor cannot page by a nullable column"}`. The server minted a token it will refuse.
- **(4)** is worse and the obvious three-line fix for (3) would not close it. `Field.Optional` is true only for `crud.Opt`, because `isOptType` requires an unexported interface (`crud/meta.go:503`). So a `LastSeen sql.NullTime` — the shape an ent or gorm struct actually carries, and the shape [[UC-010]] promises to adopt — is *not* Optional and *not* a pointer. The refusal at `crud/cursor.go:124` does not fire, `ElemValue` passes the value through unchanged (`crud/access.go:87`), and the comparison binds through `sql.Null`'s `Valuer`. The walk returns 200 and silently never shows a row whose column is NULL; a page whose last row is NULL ends the feed early. No error, anywhere.

**(5)**: a sort that crosses a relation — `sort=author.name` with the key appended, an ordinary feed sort — returns silently at `crud/sqlrepo/repository.go:248`, so the response has no `nextCursor` and nothing says why. `sqlrepo.UnstablePagination()` does the same thing for a different reason (H-CRUD-07 (5)). The capability drops out with no signal. (The `UnknownFieldError` at `crud/cursor.go:119` is not what a consumer meets: `decodeCursor` runs first at `:109`, and since the mint side never produces a token whose fields name a relation path, that branch is reachable only from a hand-built `crud.EncodeCursor` or a forged token.)

The nullable refusal at `crud/cursor.go:126` has **no test anywhere in the tree**; a grep for its text finds one line, the line itself.

**Blast radius, and it is wider than infinite scroll:** `setCursors` runs on both branches of `Get` — the `NoTotal` one at `:206` and the counted one at `:228` — so **every** paged list sorted on a nullable column emits `nextCursor` in its JSON, whether or not the consumer ever opted into cursor paging. Anyone with a `deleted_at`, `published_at` or `last_seen_at` sort is shipping a token they do not know is there. `crud/page.go:14`'s own doc comment says the cursors are "set only on a cursor walk", which is stale and is probably why this reads narrower than it is.
**If not ready:** Today the consumer sorts by a NOT NULL column and never learns why the other one failed. Two changes and they have to ship together, or (3) turns into (5): the mint side must not emit an edge when any sort field can be NULL — and "can be NULL" has to mean `Optional`, pointer, *and* a type whose zero value is a valid NULL through its `Valuer`, or `sql.NullTime` walks straight past the new predicate the same way it walks past the old one. Shipping that alone makes infinite scroll over `published_at` stop working with no `nextCursor` and no explanation, so it needs the (5) signal beside it: a way for a caller to know the page has no cursor by design rather than by accident, covering the relation sort and `UnstablePagination` too. The refusal at `:126` also needs the test it never got.

### H-CRUD-09 — The list screen must not read the 2 MB `body` column
**Who:** the engineer whose list endpoint got slow after the article body grew
**Wants:** to name the four columns the table shows
**Story:** They add a projection to the list read and leave the detail read alone. The rows still address correctly, the preloads still attach, and the response is a tenth the size.
**Must hold:**
1. Naming some columns returns those columns, and the row is still addressable — the key comes back whether or not they asked for it.
2. A preload still attaches, because whatever it joins on comes back too.
3. A projection that cannot work with something else in the same read is refused before the statement is sent, not by the driver.
4. A name that is not a column is refused, and no statement runs.
5. A model loaded through a projection is not a model that can be saved.

**Today:** 🟡 partial — (1)-(4) hold; (5) is the trap and nothing marks it
**Evidence:** (1) `crud/sqlrepo/repository.go:450` adds the primary key back to any non-`DISTINCT` projection; (2) `:458` adds each preload's local join column. (3) is [[D-024]]'s territory and the refusals are real: a sort the projection cannot cover, a relation sort, a preload under `DISTINCT`, each refused rather than widened, pinned across `crud/sqlrepo/paging_edge_test.go` and against both engines at `test/integration/dialect_edge_test.go:608-627`. (4) `crud/sqlrepo/repository.go:444` and `:454` are `UnknownFieldError` before `Done()`.

(5) is H-CRUD-03 (6) seen from the projection side, and it is the easiest of that defect's three symptoms to hit by accident: `Select` leaves every unselected field at its zero value, and `Save` on that same model takes the upsert branch, so the ordinary shape — load a light row, change one field, save it — writes zeros over the columns that were never fetched, with a 200. Nothing in `Select`'s doc comment (`crud/options.go:155`), in `Save`'s (`crud/repo.go:22`) or in the reference connects the two.

One more thing this case has to carry, because it is the option group's own open question: **`crud.Distinct()` with no projection deduplicates nothing.** Every row differs by primary key, which the full projection always includes, so the keyword costs a sort or a hash and removes no rows and says nothing. That is [[D-024]], status **open**, and it is in the decisions index's Open tensions list with three ways out and none chosen.
**If not ready:** For (5) the consumer either never saves a projected model — a rule nobody can enforce — or re-reads the row first, which gives back the round trip the projection saved. The cheap half is a sentence in `Select`'s doc comment. The real answer is blocker 3.

### H-CRUD-10 — Filter across a relation, and load the detail page in one go
**Who:** the engineer building "articles by this author, with their comments"
**Wants:** relation filters and eager loading without a join and without N+1
**Story:** They filter on `Author.Name`, sort on it, and preload `Author` and `Comments.Author` for the detail view. They never write a join, and they never see the row count change because an article has two matching tags.
**Must hold:**
1. A filter through a relation does not multiply the result set: `LIMIT 20` still returns twenty articles and `COUNT` still reports a number that exists.
2. A path may cross more than one hop, and a client's spelling (`author.name`) means the same as the model's.
3. Preloading is one statement per relation per level, whatever the page size.
4. Sorting through a collection is refused rather than resolved by picking a row.
5. Pagination inside a preload is refused rather than truncating some parents' children and not others.
6. An option a preload cannot honour is refused, not accepted and ignored.

**Today:** 🟡 partial — (6) fails across eleven options
**Evidence:** (1) `crud/predicate.go:86` renders each hop as a correlated `EXISTS` ([[D-005]]), pinned at `crud/predicate_test.go:149` and `crud/schema_edge_test.go:371`. (2) `WalkPath` at `crud/relation.go:356` is the single resolver, and the fold at `crud/meta.go:145` makes `author.name` and `Author.Name` one path; an alias that would be ambiguous resolves to nothing rather than to a guess (`crud/meta.go:162`, pinned at `crud/schema_edge_test.go:304`). (3) `crud/preload.go:221` batches, chunking at 900 keys ([[D-006]]), pinned at `crud/preload_test.go:41`, `:286`. (4) refused at `crud/predicate.go:554`, pinned at `crud/predicate_test.go:297`. (5) refused at `crud/preload.go:193`, pinned at `crud/preload_test.go:328`.

(6) is the gap, and it is wider than a projection. Inside `PreloadWhere` the preloader consults exactly three things: the paging fields, to refuse them (`crud/preload.go:193`), the predicate (`:268`) and the sort (`:299`). **`Fields`, `RelScopes`, a nested `Preloads`, `Distinct`, `ForUpdate`, `Primary`, `NoSort`, `NoTotal`, `After`, `Before` and `Agg` are all accepted and dropped** — eleven, and two of them are not projections:
- `crud.PreloadWhere("Comments", crud.NarrowRelations(rs))` is a **narrowing** that narrows nothing. The preloader reads only the root `p.scopes` (`crud/preload.go:128`, `:267`, `:274`, `:291`) and never `o.RelScopes`. A caller who believes they have constrained the far side of a preload has not, and that is a different severity class from a dropped `Select`.
- `crud.PreloadWhere("Comments", crud.Preload("Author"))` — a nested preload — is dropped too. The working spelling is the dotted path `Preload("Comments.Author")`, and nothing says the other one does nothing.
- `crud.PreloadWhere("Author", crud.Select("Name"))` still selects every column of the target (`:276` and `:292` both write `target.Fields`), so the big column comes back anyway.
- `crud.PreloadWhere("Author", crud.Unsorted())` still gets the default sort at `:307`. **This one only shows on a `has_one`**: the default is guarded by `if len(sort) == 0 && rel.Kind == HasOne` at `:300`, so on a `has_many` the sort list stays empty and `OrderBy(nil)` renders nothing. The option is dropped in both cases; only the has_one makes it visible.
- `crud.PreloadWhere("Comments", crud.After(tok))` vanishes without a word — `After`/`Before` are not in the paging refusal's list at `:193`.

Paging inside a preload is refused loudly eighty-three lines above the first of these; nothing else is.
**If not ready:** They eat the column, or they stop preloading and issue their own second query — which is the query that then runs outside the relation narrowing ([[D-007]]). The cheap fix is symmetry: refuse everything a preload cannot honour the way pagination is refused, which is a list of eleven and not of one. The better one is to honour the projection and the narrowing, keeping the join column the way the root projection does at `crud/sqlrepo/repository.go:458`.

Also unclosed and not a bug: **there is no per-parent limit.** "The last three comments per article" is one of the commonest list-detail asks, and the refusal at `:193` sends the consumer to hand-written SQL — the outcome (6)'s "if not ready" exists to avoid. A lateral join or a window function would express it and both are dialect-divergent, which is [[D-019]]'s territory; this sweep does not propose it, only that it be named as out of scope rather than read as closed.

### H-CRUD-11 — The filter that is not one of the twenty-six
**Who:** the engineer in month two, with a jsonb containment test, a full-text match or a vendor operator
**Wants:** one predicate the vocabulary does not have, without leaving the vocabulary
**Story:** They reach for the escape hatch, drop a fragment in beside their ordinary filters, and keep the tenant scope, the soft-delete filter and the gate's narrowing exactly where they were.
**Must hold:**
1. There is an escape hatch, and using it does not mean abandoning the option list.
2. Values still bind. The escape hatch is not string concatenation, and it is portable across the engines.
3. It is ANDed like any other predicate, so it cannot widen a scope somebody else installed.
4. What it gives up is visible at the call site and reviewable.

**Today:** 🟡 partial — (1)-(3) hold; (4) is where a rename dies at request time
**Evidence:** `crud.Raw` is one of the 26 predicate constructors (`crud/predicate.go:480`) and goes into `crud.Where` like any other, so (1) and (3) are free — `Where` ANDs ([[D-004]]) and there is no node that removes one. (2) holds and holds well: `?` is rewritten to the dialect's bind marker and `??` escapes a literal question mark (`crud/predicate.go:366-391`), and a mismatch between markers and arguments is a `SchemaError` in both directions (`:375`, `:387`) — because whoever wrote a native `$1` by hand would otherwise get it renumbered against somebody else's bind. Exercised across dialects at `test/integration/dialect_edge_test.go:706`.

(4): column names in a raw fragment are neither resolved nor quoted. That is the price and it is stated — `docs/modules/en/crud.md:146-147` calls it "the one thing to grep for in review", and `README.md`'s Sharp edges repeats it. What it means in practice is that `Raw` is the one predicate a renamed column does not break at start-up and does not break at the generated metamodel either: it breaks at request time, as a driver error, on whichever endpoint used it. It is one of two holes in this file's "a field-name typo is caught" story; the other is H-CRUD-01 (4).
**If not ready:** Nothing to write by hand — the hatch works. What is missing is the counterpart the closed AST has everywhere else: no way to say "resolve this name, then let me write the operator", so the choice is the whole check or none of it. [[D-003]] is why the hatch is one function and not a family, and this sweep does not challenge that.

### H-CRUD-12 — The number on the dashboard, under the same narrowing as the list
**Who:** the engineer adding "orders per status this month" and "top ten customers by revenue"
**Wants:** grouped summaries that cannot see another tenant's rows
**Story:** They group by status and count. Then the product asks for the top ten by revenue, and they want to sort by the sum and take ten.
**Must hold:**
1. A summary carries every narrowing a row read carries — the permanent scope, the relation scopes, whatever the gate installed.
2. Every name in the summary resolves against something this statement has, so a name that is not a grouping column is a refusal rather than a statement the database rejects.
3. The result is readable whatever shape the driver chose for the number.
4. A summary can be ranked and cut to the top N.
5. A summary can be ordered by what it computed.
6. A summary can be filtered on what it computed — "statuses with more than five orders".
7. A summary that was not asked to be paged returns every group.

**Today:** ❌ missing on (4), (5) and (6); (2) and (7) fail
**Evidence:** (1) is the reason the method is on the seam at all: `crud/repo.go:39` and [[D-029]], with the rationale spelled at `crud/aggregate.go:11` — a `GROUP BY` written by hand is the query that counts another tenant's things. (3) `AggregateRow.Int`/`Float` at `crud/aggregate.go:56`, pinned at `crud/aggregate_test.go:26`, `:58`, `:105`.

(4) is **not** present, and round 1 said it was. `crud.Limit(10)` is resolved and applied (`crud/sqlrepo/repository.go:1051`), but with no way to rank, ten arbitrary groups is not a top ten. A consumer reading "Limit works" ships a dashboard whose "top ten customers" is whatever the planner emitted first. (4), (5) and (6) are one missing capability, not three.

(5): the sort in an aggregate read is resolved against the *model* (`crud/sqlrepo/repository.go:1048`), and `Order` resolution ends at `crud/predicate.go:539` with `cur.meta.Field(...)`. So `crud.OrderBy(crud.Desc("revenue"))` over `crud.Sum("revenue", "Amount")` is an `UnknownFieldError`. Loud, but the capability is absent. (6): there is no `Having` anywhere in the package — `AggregateSpec` is aggregations plus grouping (`crud/aggregate.go:68`).

(2) fails on the half nobody validates. `AggregateSpec.Validate` (`crud/aggregate.go:87-140`) checks the aggregations and the `GroupBy` and never sees `o.Sort`; `repository.go:1048` renders `o.Sort` straight through and `sortExpr` resolves it against the model, which succeeds for any real column. So `orders.Aggregate(ctx, crud.GroupBy("Status"), crud.Aggregate(crud.CountAll("n")), crud.OrderBy(crud.Desc("Amount")))` emits `ORDER BY "amount"` and PostgreSQL answers 42803 — a 500 for a statement this package built, which is exactly what the comment at `:1046` ("a sort is honoured only over the grouping columns") describes as the intent and nothing enforces. It is the same failure the sibling refusals inside `Validate` were added for, one argument over.

(7) is the quiet one, and it is worse than (5) because a wrong number on a dashboard is not obviously wrong. `crud/sqlrepo/repository.go:1051` applies `Resolved(defaultLimit, maxLimit)` whether or not the caller asked for a page, and `DefaultPageSize` is 20 (`crud/sqlrepo/blueprint.go:26`, defaulted at `:175`). So a group-by over 21 statuses returns 20, with no `Total`, no flag and no error. `crud.Unpaged()` is the escape (`:1053`), and nothing points at it. This is Sqlrepo blocker 8, same line, one fix.

`Aggregate` also drops seven options it was handed — `Fields`, `Preloads`, `Distinct`, `ForUpdate`, `After`, `Before`, `Primary` (`crud/sqlrepo/repository.go:1028-1055` reads none of them) — which is H-CRUD-06 (5).
**If not ready:** "Top ten customers by revenue" is written as raw SQL, and raw SQL is outside the scope, the relation scopes and the gate — the precise failure `crud/aggregate.go:11` says aggregates exist to prevent. Closing (4), (5) and (6) is one design question: see **Why this shape** for the namespace it has to answer and the refusals it has to carry. Closing (2) is one comparison against `Agg.GroupBy` before the statement is built. Closing (7) is a default, and the honest one is that an aggregate with no explicit paging is unpaged — a summary is not a page.

### H-CRUD-13 — Run inside the transaction somebody else already opened
**Who:** the engineer adopting this beside an ORM, in a service that also talks to an analytics database
**Wants:** their existing transaction to cover this library's writes, and only the right database
**Story:** They are inside `db.Transaction(...)`. They push the transaction into the context, and the repository writes land in it. Elsewhere, one process holds two databases, and a write to the second must not be captured by a transaction on the first.
**Must hold:**
1. Joining a foreign transaction is one call, and every repository call under that context runs on it.
2. Naming a database restricts the capture to repositories bound to it; the handle and any source built over it name the same database.
3. Naming the *wrong* thing is a refusal at the call, not a silent no-op.
4. A transaction this library opens itself is scoped the same way, without the caller writing anything.
5. An inner transaction joins the outer one rather than nesting inside it, so the outer owner keeps commit and rollback.
6. If they wrap their pool for tracing and forget one method, their two databases do not quietly stop being separable.

**Today:** 🟡 partial — (3) fails, and the failure is a write that reports success
**Evidence:** `WithExecutor` at `crud/executor.go:316` ([[D-009]] — the capture is unconditional and has to be), `WithExecutorFor` at `:335`, the innermost-binding walk at `bindingFor` (`:389`) where a scoped binding cannot hide an unscoped one from another repository. `InTx` at `:503` joins when there is already an executor for that source, and scopes what it opens through `ownScope` at `:439` — whose comment names the accident: with a bare assertion instead of the walk, a wrapped source opened its transaction and then bound it unscoped, so every repository in the process adopted it. `identityOf` at `:448` is (6)'s fallback: an unidentified wrapper in front of two databases makes them inseparable again rather than failing, which matters only because [[D-041]] refuses an unidentified source at start-up on the path that can. Pinned by `crud/executor_test.go:72`, `:88`, `:101`, `:141`, `:166`, `:190`, `:204`, and `crud/wrapsource_test.go:180`. [[UC-012]] and [[UC-005]] cover it end to end; [[D-027]] holds the residual tension open.

(3) fails and **this module owns it.** `crud.WithExecutorFor(ctx, tx, ...)` — naming the *transaction handle* rather than the database — compiles, keys the binding on something no repository matches, and is therefore ignored: the write goes to the pool outside the transaction and reports success. It is identical at the call site to the correct call. The sibling sweep is ✅ here and hands the failure back in writing — "it is owned by `crud/executor.go:335`, not by this module" (`docs/ai/usecases/modules/sqlrepo/Sqlrepo.md:522-529`), repeated in its blocker 16 as "not fixable here". Round 1 of this file cited a 🟡 there that does not exist, so the gap was claimed by neither sweep. It is claimed here. It is also open tension 18 in `docs/ai/usecases/Index.md`.
**If not ready:** Nothing a consumer can write catches it — the call they made is the call they meant to make, spelled one argument wrong. `WithExecutorFor` refusing a `ds` that no bound repository can match, or saying so through the logger seam ([[D-062]]), turns a silent misroute into a start-up complaint. The refusal is the safer of the two: a binding nobody matches is never intentional.

### H-CRUD-14 — Reads on the replica, decisions on the primary
**Who:** the engineer whose read traffic is ten times the write traffic
**Wants:** one line to add a replica, and no correctness cost
**Story:** They pair the two handles and bind the repository to the pair. Reads move. Then somebody points out that the load half of an update is also a read.
**Must hold:**
1. Pairing is one call.
2. A read inside a transaction goes to that transaction, not to the replica.
3. A read whose answer decides a write stays on the primary.
4. A caller can force one read onto the primary.
5. Pairing does not cost transactions.

**Today:** 🟡 partial — (3) has one hole, and it is on the path a consumer reaches under contention
**Evidence:** `ReadWrite` at `crud/executor.go:83`, with the two non-negotiables written into the doc comment and enforced in the router at `crud/sqlrepo/repository.go:109`: the context binding wins, then `Options.Primary` (`crud/options.go:173`), then the replica — [[D-032]]. (5) is why there are two types rather than one that sometimes refuses (`crud/executor.go:88`): a caller asking `src.(crud.Beginner)` is asking whether transactions work, and a wrapper that says yes and then says no has lied about the pool behind it. Run against two databases holding *different* rows, so the answer names which one replied: `test/integration/replica_test.go:24`, `:82`, `:96`, `:138`, `:187`.

The hole: `missedRow` (`crud/sqlrepo/repository.go:817`) calls `r.Exists(ctx, …)` with **no** `crud.PrimaryOnly()`, so it routes through `read()` and lands on the replica whenever there is no transaction. Its answer decides `ErrNotFound` (the caller stops) versus `ErrStaleVersion` (the caller retries) — H-CRUD-05 (4). On a replica that is behind, a row that was just deleted still exists and the caller retries forever; a row that was just inserted does not, and a live conflict is reported as a vanished row. [[D-032]] is explicit: "If a new such read appears, it gets `PrimaryOnly` in the same change." **This is H-SQLREPO-09 (`docs/ai/usecases/modules/sqlrepo/Sqlrepo.md:402-434`) and Sqlrepo blocker 3, at the same severity — one fix, counted once.** It is listed here because a `crud` consumer reading only this file would otherwise not know their retry loop can be told the wrong thing.
**If not ready:** One option on one call. The residual that is genuinely the caller's is stated rather than solved: write, then read in a separate call before the replica catches up, and the row is missing. `PrimaryOnly()` is the answer to that one.

### H-CRUD-15 — Time every statement without losing what the source could do
**Who:** the platform engineer adding tracing to a service already in production
**Wants:** a wrapper around the datasource
**Story:** They write a dozen lines: `Exec`, `Query`, `Dialect`, each timing and delegating. They deploy. Nothing errors.
**Must hold:**
1. A wrapper is four methods, and the fourth — `UnwrapSource` — is the only one the compiler will not ask for.
2. A wrapper keeps everything the wrapped source could do — transactions, the replica, the identity the catalog is keyed on.
3. When it does not, they find out at start-up rather than from a replica that quietly went idle.

**Today:** 🟡 partial — (1) and (2) are provided; (3) holds for two of the three losses
**Evidence:** `SourceUnwrapper` at `crud/executor.go:224`, and the three walks that follow it — `BeginnerOf` (`:254`), `ReadSourceOf` (`:268`), `identityOf` (`:448`). Pinned by `crud/wrapsource_test.go:56`, `:106`, `:141`. [[D-061]] is the decision and names the live failure it came from.

The obligation is real and unchecked: omitting `UnwrapSource` costs `Beginner` (loud — a transaction refuses), `Identified` (loud — [[D-041]] refuses at start-up) and `ReadSourcer` (**silent** — every read goes to the primary, and nothing connects that to the day the wrapper was added).
**If not ready:** There is no mechanism to add — [[D-061]] explains why a generic forwarding wrapper is not expressible in Go. What can be closed is the silence, and where it can be closed is a real choice rather than an obvious one. The check itself is a walk over `Next()`, which lives in package `crud`, and `crud` imports the standard library only ([[D-016]]) while `port` imports `crud` — so `port.Logger` is unreachable from there in both directions at once. The place it fits is `Bind` in `crud/sqlrepo`, which would be the first import of `port` by anything under `crud/` that is not a transport. That is worth a decision doc rather than a patch; the alternative that costs nothing architecturally is for `Bind` to refuse, which is harsher than the failure deserves.

### H-CRUD-16 — Put a rule above the repository that nothing can go round
**Who:** the engineer who has to audit every write, or soft-delete, or refuse a verb for a role
**Wants:** one place their rule runs, that a later caller cannot step past
**Story:** They write a decorator, embed the seam, override the one method their rule is about, and wire it in. Later they add the error probe above it, and the two compose.
**Must hold:**
1. Every write is on the seam, including the bulk ones — a caller cannot get to a summary or a batch save without passing the decorator.
2. Wiring order is a property they can state, not one they have to discover.
3. The error probe wired above their audit decorator still finds the datasource — or the process refuses to start.
4. The reference shows the shape that satisfies (3).

**Today:** 🟡 partial — (1)-(3) hold; (4) teaches the opposite
**Evidence:** (1) `Aggregate` and `SaveAll` are on `Core` precisely so a decorator that checks writes has to see them (`crud/repo.go:39`, `:43`), and so are `Delete`, `DeleteAll` and `UpdateAll` (`:45`, `:47`) — [[D-030]] makes each new verb an obligation the gate must override or justify, enforced by a test that will not compile until one of the two is true. (2) `Decorate` and `Chain` put the first middleware outermost (`crud/repo.go:104`, `:110`), stated in the doc comment. (3) `SourceOf` walks `Nexter` (`crud/executor.go:195`) and `Base` supplies `Next()` for free (`crud/repo.go:121`), pinned at `crud/basenext_test.go:48`, `:91`; a probe above a decorator with no `Next()` panics at start-up with a message naming the fix (`crud/decorators/faults/probe.go:94`).

(4) fails: `README.md:501` teaches `type auditing struct{ crud.Core[User, int64] }` with no `Next()` — the exact erasure [[D-061]] exists to prevent, in the first snippet a consumer copies. The module reference says the opposite one page over (`docs/modules/en/crud.md:317`: "Embed `crud.Base`"), and a consumer reading both has no way to know which is current.
**If not ready:** Nothing to write by hand. Change the README example to embed `crud.Base[User, int64]` and say in one line why: an embedded interface promotes only its own method set, so the chain underneath goes invisible to anything that walks it. It is the same edit pass as blockers 23 and 25.

### H-CRUD-17 — Branch on failures without matching strings
**Who:** every consumer, in the handler and in the retry loop
**Wants:** to tell "no such row" from "somebody else got there first" from "you sent something I cannot use"
**Story:** They write `errors.Is(err, crud.ErrNotFound)` for the 404, `crud.ErrConflict` for the 409, and `crud.ErrStaleVersion` for the retry. Later they add the error subsystem, and none of those branches change.
**Must hold:**
1. The failures a caller routes on — missing, conflict, stale, forbidden — are sentinels, and comparison is `errors.Is`.
2. A stale version is distinguishable from a vanished row: retry versus give up.
3. A sentinel survives being wrapped by the error subsystem.
4. A field name that does not resolve fails the whole request before any SQL runs, and carries the name in a typed error.
5. The value of an unresolvable filter never reaches the statement.
6. A failure caused by something the *client* sent is distinguishable in Go from one caused by the declaration, without reading a message.

**Today:** 🟡 partial — (4) has a hole, (6) fails and it is the branch a handler needs
**Evidence:** Seven sentinels, the complete list, at `crud/errors.go:8-36`; `ErrStaleVersion` wraps `ErrConflict` at `:36` so a transport answers 409 without knowing about versions ([[D-015]], [[D-038]]). (4) `UnknownFieldError` at `crud/errors.go:41`; the writer remembers the first failure and the builder surfaces it (`crud/render.go:132`, `:138`), and every statement in `crud/sqlrepo/repository.go` goes through `Done()`. (5) is pinned with the control that matters — `crud/edge_test.go:165` asserts across eight predicate shapes that no argument carrying the value reaches the statement. [[D-013]] is the decision.

(4)'s hole is the empty value list. `inNode.render` short-circuits to `1 = 0` (or `1 = 1` for `NotIn`) at `crud/predicate.go:201-210` **before** `w.leaf` resolves the path, so `crud.In("Typo")` with no values produces no `UnknownFieldError` and no complaint of any kind — the typo becomes a constant. `crud/edge_test.go:165` covers only the non-empty shape. It is small, and it is the one place [[D-013]] does not hold.

(6): `SchemaError` (`crud/errors.go:52`) has no `Is`, no `Unwrap` and no code, and its own doc comment at `:51` still says it "is raised eagerly by Define/New so a broken mapping fails at start-up" — which stopped being true. It is now also what *client-supplied input* produces at request time: `crud/cursor.go:60`, `:64`, `:67` ("not a valid cursor"), `:71`, `:76` ("this cursor was made for a different sort order"), `:126` (a nullable cursor column), `crud/preload.go:195` (a preload cannot be paginated), `crud/predicate.go:556` (cannot sort through a relation). `UnknownFieldError` has no `Is` either. The transport is fine — `port/kind.go:123-125` maps both to `KindBadRequest` — but the Go-level branch this case is named for is not there, and the only discriminator inside a `SchemaError` is `Reason` text.
**If not ready:** A consumer type-asserts `*crud.SchemaError` and reads `Reason`, which is comparing strings with extra steps. An `Is` on both types against an `ErrBadInput` sentinel, or a `Code` field on `SchemaError`, closes it without touching the seven sentinels. The stale doc comment should go in the same change.

### H-CRUD-18 — `DELETE /articles/42`, and the cleanup screen that deletes forty
**Who:** the engineer wiring the delete endpoint, and the author of a bulk archive job
**Wants:** the row gone, an honest answer when it was not there, and no way to delete the whole table by forgetting an argument
**Story:** The handler deletes by id and wants a 404 when the id names nothing. The cleanup job deletes everything older than a year. Later the product says nothing may ever really be deleted, and they add a tombstone column.
**Must hold:**
1. Deleting an id that names no row is distinguishable from deleting one that did.
2. A row this repository is not allowed to *see* cannot be deleted by id either.
3. A filtered delete carries the same narrowing the equivalent read carries.
4. A filtered delete with no filter is not the shape a typo produces.
5. Turning on soft delete changes both halves at once — what a delete does and what a read sees — because declaring one without the other is how a "deleted" row comes back on the next list.

**Today:** 🟡 partial — (1) is left to the caller and nothing says so; (4) fails
**Evidence:** (2) and (3) hold and the comments say why they had to: `Delete` ANDs the permanent scope into the id predicate (`crud/sqlrepo/repository.go:873-886`) — "without it a row the repository refuses to show is still deletable by id — `GET /:id` answers 404 and `DELETE /:id` answers 200 for the same row" — and `DeleteAll` builds `Where(r.scoped(o))` with the per-request relation narrowings (`:903-918`). (5) is [[D-031]]: `sqlrepo.SoftDelete` folds the "not deleted" test into the permanent scope at declaration (`crud/sqlrepo/blueprint.go:111`), so the two halves cannot be added separately, and `stamp` (`:933`) takes the narrowings as a parameter rather than reading the blueprint — the comment names the bug that cost: two repositories differing only in `SoftDelete` produced a narrowed DELETE and an unnarrowed UPDATE for the same call.

(1): `Delete` returns `(0, nil)` for an id that names no row — `res.RowsAffected` and nothing else (`crud/sqlrepo/repository.go:895-901`). That is defensible, and it is also invisible: a `DELETE /:id` that wants a 404 has to compare the count, and `crud/repo.go:45`'s doc comment ("removes rows by id and reports how many went away") does not tell a reader that the count is the only signal there is.

(4): `articles.DeleteAll(ctx)` on a repository with no `Scope` renders an unfiltered `DELETE FROM articles`. There is no refusal for an empty option list, and "I meant to pass the filter and the variable was empty" produces exactly that call. `crud.In("ID", ids...)` with an empty `ids` is safe by accident (`1 = 0`, H-CRUD-17 (4)) and `crud.NotIn` with an empty list is the opposite (`1 = 1`) — three spellings of "no filter", two of which take the table.
**If not ready:** For (1) they compare the count and hope the next person does. For (4) they write the guard themselves: `if len(opts) == 0 { return }`. A `DeleteAll` that refuses an empty predicate unless the caller says so explicitly would close it and would be a breaking change on a verb nobody calls empty on purpose — worth a decision, cheap before a tag and expensive after.

### H-CRUD-19 — The badge, and "is this email taken"
**Who:** the engineer with a dashboard tile and a signup form
**Wants:** a count and a yes/no that mean the same thing the list beside them means
**Story:** The tile shows "42 open tickets" under the same tenant filter the list uses. The signup form asks whether the email is free before it writes.
**Must hold:**
1. A count carries every narrowing the list beside it carries — the permanent scope, the relation scopes, whatever the gate installed.
2. A count under a projection or a `Distinct` counts what the list would show, not the rows behind it.
3. A yes/no is one statement, not a page fetched and measured.
4. Using one as a pre-flight before a write is safe, or the reference says it is not.

**Today:** 🟡 partial — (4) fails, and it is the shape this library's own missing verb pushes people into
**Evidence:** (1) `Count` and `Exists` both build through `r.scoped(o)` with `r.relScopes(o)` (`crud/sqlrepo/repository.go:559-604`), so a decorator's narrowing reaches them; `gate` overrides both (`crud/decorators/security/security.go:376`, `:387`) per [[D-030]]. (2) holds and the comment is the reason it exists: a bare `count(*)` under a `DISTINCT` projection "would count the rows the SELECT DISTINCT is about to collapse, and Get would then hand the client a total — and a page count — for pages that do not exist" (`:562-566`), so the portable spelling is a derived table. (3) `Exists` is `SELECT 1 … LIMIT 1` (`:589`).

(4) fails, and not because `Exists` is wrong: check-then-act is check-then-act. It matters here because H-CRUD-03 (6) leaves a create endpoint that must not overwrite with no other option, so this library actively routes consumers into the race. Nothing in `Exists`'s doc comment (`crud/repo.go:51`) or the reference says the pre-flight is not a guarantee, and unlike everywhere else in this package the failure is not "you get an error" but "you get a 200 and one row where there were two writers".

Both verbs route through `read()` (`:583`, `:594`), so on a `ReadWrite` source they answer from the replica unless the caller passes `crud.PrimaryOnly()` — right for a badge, wrong for a pre-flight, and the same class as H-CRUD-14's hole with the difference that here the caller chose the read.
**If not ready:** The right answer is the database's: a unique constraint and `ErrConflict`. The reference should say that in one line where `Exists` is documented, because a consumer who reaches for `Exists` before a `Save` has usually already decided not to add the constraint.

### H-CRUD-20 — Twenty thousand rows from a supplier file
**Who:** the engineer writing the nightly import
**Wants:** one call, not twenty thousand
**Story:** They read the file, build the models, and hand the slice over. Some rows are new and some already exist. It runs at 3am and nobody watches it.
**Must hold:**
1. A batch is one call, and the number of statements does not grow with the batch.
2. A batch too large for the driver is chunked, or the limit is documented where the call is.
3. A batch mixing new rows and existing ones is either handled or refused — never half-written.
4. What the batch leaves in the caller's models is the same thing a single write leaves.

**Today:** 🟡 partial — (2) fails and (4) fails on one dialect
**Evidence:** (1) and (3) hold. `SaveAll` builds one `INSERT … VALUES (…), (…), …` with the shared conflict tail (`crud/sqlrepo/repository.go:1124-1176`), and it refuses a mixed batch by name — "SaveAll cannot mix rows with and without a key: they are two different statements, and splitting the batch would hide the cost" (`:1147-1149`). It is on the seam for the reason [[D-030]] gives, so a decorator that checks writes sees it.

(2) fails: there is no chunking. Twenty thousand rows over a fifteen-column model is 300 000 bind parameters, and PostgreSQL's protocol limit is 65 535 — a driver error naming nothing about vv, at 3am. The library already chunks at 900 keys in the preloader for exactly this reason ([[D-006]], `crud/preload.go:221`), so the shape exists and is not applied to the only bulk-write verb. `Delete(ids...)` has the same ceiling. This is Sqlrepo blocker 4 and is one fix in `crud/sqlrepo`; it is here because the ceiling is invisible from `crud/repo.go:43`, which is where a consumer decides to call it.

(4): on a dialect without `RETURNING` the batch comes home with no keys and no `generated` columns, while a single `Save` of the same row has both — Sqlrepo blocker 5, and it breaks [[D-011]]'s "the model describes the row" for the batched twin only.

`SaveAll` is also an upsert row for row, so blocker 3 applies here at batch scale: an import that collides on twenty thousand client-supplied keys overwrites twenty thousand rows and reports success.
**If not ready:** They chunk it themselves, in a loop, having first found the limit by hitting it. The number to chunk at is not knowable from outside — it depends on the column count — so this one genuinely cannot be written correctly by a consumer.

### H-CRUD-21 — The filter built from a form nobody filled in
**Who:** the engineer whose search endpoint takes six optional parameters
**Wants:** an unset parameter to mean "do not filter on this"
**Story:** They map each query parameter onto a predicate. `?status=open&status=closed` becomes an `In`. Nothing at all becomes nothing at all. Then somebody asks why the archived rows are missing from "everything except drafts".
**Must hold:**
1. A helper that has nothing to add contributes nothing.
2. An empty value list behaves the way the reader expects, and the reference says which way that is.
3. A filter value held in the same optional type this library tells them to use in their DTOs does what it looks like it does.
4. "Not equal to X" and "not in this set" account for the rows whose column is NULL, or the vocabulary offers a way to say what the caller meant.

**Today:** ❌ missing on (3); 🟡 on (2) and (4)
**Evidence:** (1) holds: `Build` skips a nil option and `Where` skips a nil predicate (`crud/options.go:68`, `:88`).

(2) is defined, asymmetric and undocumented: an empty `In` renders `1 = 0` and an empty `NotIn` renders `1 = 1` (`crud/predicate.go:201-210`). Both are the mathematically right answer and neither is what a handler that forgot to guard the parameter wanted — one returns nothing, the other returns the table. `crud.Where(nil)` returns everything, which is a third answer to a call that looks like the same mistake.

(3) is the sharp one, because this library's own design produces it. [[D-002]] tells consumers to hold `crud.Opt[T]` in their DTOs, so `crud.Eq("Age", f.Age)` with an `Opt` field is the natural next line — and it binds the `Opt` straight through. `isNil` (`crud/predicate.go:495-505`) tests only pointer, map, slice, interface, func and chan kinds, so it never sees an `Opt`; `bind` (`:154-157`) appends the raw value; and `ElemValue`, the unwrapper, is called from `preload.go`, `cursor.go`, `policies.go` and `probe.go` and **never from the predicate path** (`grep -rn "ElemValue(" --include="*.go" .`). The driver then calls `Opt.Value()`, which returns `nil` for anything not set (`crud/opt.go:105-108`), so the statement is `age = NULL` and matches nothing. No error, no refusal, a silently empty result — the exact failure class this file exists to catch, on the type this library hands the consumer.

(4) is ordinary three-valued logic and the vocabulary has no shorthand for it: `Ne("Status","archived")` is `status <> $1` (`crud/predicate.go:171-177`) and `NotIn` is `NOT IN`, and neither returns a row whose column is NULL. `crud.Or(crud.Ne(...), crud.IsNull(...))` is the spelling, and it is nowhere in the reference. This is the commonest wrong-result bug in any filter DSL and the one a consumer is least likely to suspect.
**If not ready:** For (3) they write `if f.Age.IsSet() { … }` around every filter — the guard (1) exists to remove — after finding out from an empty page. The fix is one call: `bind` runs the value through `ElemValue` the way the preload and cursor paths already do, and an undefined `Opt` in a comparison becomes a refusal rather than a NULL bind, because "filter on nothing" is never what `Eq` means. For (2) and (4) the fix is two sentences in the predicate reference.

### H-CRUD-22 — One repository value, every request
**Who:** anyone who followed the reference and declared the repository at package level
**Wants:** to not think about it
**Story:** They write `var Articles = sqlrepo.Define(...)` in a package, bind it once in `main`, and hand the bound value to every handler. Traffic arrives on a thousand goroutines.
**Must hold:**
1. One bound repository, shared by every goroutine in the process, is safe.
2. The metadata every repository over a model shares is safe to build concurrently, including the first time.
3. Declaration order does not change behaviour — a repository declared in a later `init` or in `main` is not a different repository from one declared first.

**Today:** ❓ unverified on (1) and (2), 🟡 on (3) — and nothing anywhere claims any of them
**Evidence:** The shape is process-global by construction: `schemaCache` (`crud/meta.go:187`), `planCache` (`crud/update.go:42`) and `tableRegistry` (`crud/relation.go:148`) are package-level `sync.Map`s, and each `*Relation` holds two `sync.Once`s (`crud/relation.go:75`, `:89`) — two, because `resolveDefaults` calls `Target`, which enters the first, and one `Once` would deadlock. The repository's own history says this is exactly where a race lives and is not found by running: `CLAUDE.md` records `Relation.resolveDefaults` writing to a shared `*Relation` outside its `Once`, found by reading. `grep -rn "concurrent\|goroutine" docs/modules/en/crud.md` returns nothing — the consumer reference makes no statement at all, and a consumer who has to ask this question today has no way to answer it short of reading `crud/relation.go`.

(3) fails in one specific and silent way. `Relation.Target` resolves the target's table inside its `Once` and caches the `*Meta` forever, reading the registry through `TableNameOf` at that instant (`crud/relation.go:95-108`, `:167-178`). `Define` is what writes that registry (`crud/sqlrepo/blueprint.go:181`). So a repository declared *later* — in `main()`, in a constructor, in a package initialised after the first request — that names a table other than `pluralise(snake(Name))` loses: the relation already froze the guess, `RegisterTable` at that point does nothing, and the preload reads the wrong table name with no error. `docs/modules/en/crud.md:170-173` states the resolution order and not that it is one-shot.
**If not ready:** For (1) and (2), nothing — the answer is probably yes and the point is that it is unclaimed. It is checkable from outside in about twenty lines: two goroutines, first use of a model, `-race`, in `crud` where no database is needed. That test plus one sentence in the reference closes it, and until then every consumer either assumes or guesses. For (3) the honest fix is for `RegisterTable` to refuse — or complain — once the type's relations have resolved, since a registration nobody can act on is never intentional.

### H-CRUD-23 — A settings blob and a tags array
**Who:** the engineer adopting a table that already has a `jsonb` column and a `text[]`
**Wants:** to know what they have to write for the two columns that are not scalars
**Story:** The table has `settings jsonb` and `tags text[]`. They put a struct and a `[]string` in the model and expect the same treatment every other column got.
**Must hold:**
1. It is possible to map a column whose Go type is a struct or a slice.
2. Whatever the rule is, the reference states it — because a struct field that is silently skipped and a struct field that is a relation look identical in the model.
3. A named type over a scalar — an enum-shaped `type Status string` — is an ordinary column.

**Today:** 🟡 partial — the rules exist and are discoverable only from the source
**Evidence:** The two shapes take different paths and neither is documented. A `[]string` is not a relation candidate (`relCandidate` requires a struct element, `crud/relation.go:185-197`), so it falls through to the column path and is bound raw — whatever the driver makes of it, which for `database/sql` and a `text[]` is an error at request time and for pgx is the right thing. A struct with no `Valuer` and no `rel` tag is skipped entirely (`crud/meta.go:375`), so `Settings Preferences` reads back zero forever — H-CRUD-01 (5), and blocker 14 is the tagged version of the same silence. A struct *with* a `Valuer` is one column (`isScalarStruct`, `crud/meta.go:482`), which is the working answer for `jsonb`: implement `driver.Valuer` and `sql.Scanner` on the type and it maps like any other column. (3) holds — the column path keys on kind, not on the named type.
**If not ready:** They write the `Valuer`/`Scanner` pair, having first worked out from an empty field that they needed one. Two paragraphs in the model section of the reference — "a struct is a relation candidate unless it can speak SQL for itself" and "a slice of scalars is handed to the driver as-is" — is the whole fix, and it is the first hour of adopting any real schema.

### H-CRUD-24 — The engine is not one of the three
**Who:** the engineer whose warehouse is ClickHouse, or whose employer runs SQL Server
**Wants:** to know before they commit whether this is a three-engine library or an any-engine one
**Story:** They read that dialects are pluggable, look at the interface, and try to work out what else they would have to write.
**Must hold:**
1. A dialect written outside this package compiles and works.
2. Adding one does not fork the rest of the library.
3. The reference says what a fourth engine gets and what it does not.

**Today:** 🟡 partial — (1) and (2) hold; (3) is not stated anywhere
**Evidence:** `crud.Dialect` is six exported methods with no unexported member (`crud/dialect.go:9-24`), and the three optional interfaces are each written with an explicit "a dialect written outside this package keeps compiling" default — `OffsetLimiter` (`:26-34`), `UpsertScope` (`:36-50`) and `StatementRollback` (`:52-60`) — each defaulting in the safe direction, and each comment says which direction that is and why. A deliberately open seam, and good work.

What is not stated is the rest of the bill. `docs/modules/en/crud.md:347-360` names the three built-ins and [[D-019]]'s enumerated differences and stops. A fourth dialect gets statements; it does not get `errs/sqlerr`'s per-dialect table, so a duplicate key comes back as a driver error rather than as `ErrConflict` and H-CRUD-03 (4) stops holding, and it does not get `crud/catalog`'s introspection. "Does it support SQL Server" is a first-week question at a tag, and the honest answer — the SQL yes, the error classification no — is nowhere.
**If not ready:** They write the dialect in an afternoon and discover the error-classification half when their 409 handler stops firing. One short section in the dialect reference listing what a fourth engine gets, and what `errs/sqlerr` would need, is the whole fix.

## The DX this should have

### The call site

```go
type Article struct {
    ID        int64            `db:",pk,auto"`
    TenantID  int64            `db:",immutable"`
    AuthorID  int64
    Title     string
    Views     int
    Summary   crud.Opt[string]
    CreatedAt time.Time        `db:",generated"`

    Author   *Author   `rel:"belongs_to"`
    Comments []Comment `rel:"has_many"`
}

var Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles",
    sqlrepo.MaxLimit(200),
    sqlrepo.Scope(crud.Eq("TenantID", tenant)),
    sqlrepo.SoftDelete("ArchivedAt"))

articles := Articles.Bind(crudsql.Postgres(db))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Author.Name", "Ann")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Preload("Author"),
    crud.Limit(20))
```

This is today's code and it is close to the ideal, so this section spends its
budget on the edges rather than proposing a redesign of the middle. Four of the
seven columns need no tag at all — `snake(FieldName)` derives `author_id`,
`title`, `views` and `summary` — and three need an option-only tag whose name
half is empty. Spelling a column out is for a table whose names are not
snake_case, and that sentence is what the model reference is missing.

Three things in it are load-bearing and easy to miss. `AuthorID` is not
decoration: `belongs_to` derives its foreign key as `<Field>ID`, and without the
field the relation fails — on the first request that crosses it, not at start-up
(H-CRUD-01 (4)). `MaxLimit` is not a tuning knob: without it `crud.Unpaged()`
returns the whole table (H-CRUD-07 (7)). And `Bind` takes a `crud.Source`, not a
`*sql.DB` — `crudsql.Postgres(db)` is the adapter that says which SQL this handle
speaks, and it is an import from a second module.

`MaxLimit` being mandatory is a per-resource obligation with no per-service
spelling, and resource number twenty is one omission away from returning the
whole table. `Setting` is a plain value, so the shape that already works is worth
writing down:

```go
var house = []sqlrepo.Setting{sqlrepo.MaxLimit(200), sqlrepo.DefaultLimit(20)}

var Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles",
    append(house, sqlrepo.SoftDelete("ArchivedAt"))...)
```

The alternative — a non-zero `MaxLimit` default — is [[D-060]]'s question, which
today answers it for the wire and not for the in-process caller.

### Turning one knob

The tenant filter and the soft-delete are **not** in this section, and that is the
point. They are on the `Define` above, where `sqlrepo.Scope`'s doc comment says
they are "ANDed in before anything a caller passes, so no query option can widen
it again" (`crud/sqlrepo/blueprint.go:85`). A tenant boundary a call site can
forget is one forgotten call site away from a cross-tenant read, and this file's
own opening says so. What belongs at the call site is what the request actually
asked for:

```go
// assembled from what the request asked for, and reused by the nightly export
func published(since time.Time) crud.Option {
    return crud.Where(crud.And(
        crud.IsNotNull("PublishedAt"),
        crud.Gte("PublishedAt", since)))
}

opts := []crud.Option{published(f.Since), crud.Limit(20)}
if f.Author != "" {
    opts = append(opts, crud.Where(crud.Eq("Author.Name", f.Author)))
}
opts = append(opts, sortFor(f))          // may return nil; that is not an error

page, err := articles.Get(ctx, opts...)
```

Reaching further is one more element in a slice. A nil option and a nil predicate
are both no-ops, so a helper that has nothing to add returns nothing and the call
site does not grow a guard around it (`crud/options.go:68`, `:88`). The exception
is a value held in a `crud.Opt`: `crud.Eq("Age", f.Age)` on an undefined `Opt`
binds NULL and matches nothing, so that one *does* need the guard until
H-CRUD-21 (3) is closed.

The price of the composability is worth stating, because it is invisible at the
call site: **a `crud.Option` carries no model type parameter** (`crud/options.go:57`),
so `published` type-checks against every model in the service. On the twentieth
resource — the one whose column is spelled `LiveFrom` — it compiles, ships, and
fails at request time as an `UnknownFieldError`. That is the deliberate price of
the same untyped shape that lets one slice hold a security decorator's option
beside a handler's; the typed alternative is in the repository and the migration
is not free:

```go
func published[M any](since time.Time) specs.Specification[M] { … }

opts = append(opts, crud.Where(specs.Predicate(published[Article](f.Since))))
```

The helper's return type changes, and a `[]crud.Option` accumulator no longer
takes it directly — `specs.Predicate` (`crud/decorators/specs/spec.go:196`) is
the bridge back. That is the trade, priced.

Where the shape stops extending is at the edges of the list:

```go
// wanted, and not expressible today
revenue := crud.Sum("revenue", "Amount")

rows, err := orders.Aggregate(ctx,
    crud.GroupBy("CustomerID"),
    crud.Aggregate(revenue),
    crud.Having(crud.GtAgg(revenue, 1000)),   // does not exist
    crud.OrderBy(crud.ByAgg(revenue).Desc()), // does not exist
    crud.Limit(10))                           // runs, and means nothing without the line above it
```

`Limit` is the only line that runs today, and on its own it is not "top ten", it
is ten arbitrary groups (H-CRUD-12 (4)). Drop it and the summary is not unpaged,
it is cut to twenty (H-CRUD-12 (7)).

### Why this shape

A variadic list of function values is the only shape where "the short call" and
"the same call, longer" are literally the same call. A builder would make the
common case two lines and the composition case a question about who owns the
receiver. A struct of fields would make every option a decision the caller has to
have an opinion about — and `Options` *is* that struct, exported so decorators
can read it, which is precisely why "no option can unset a predicate" is a rule
[[D-004]] holds rather than a property the type enforces (H-CRUD-06 (3)).

Field paths as strings are the right default and not an accident: they arrive as
strings from a query parameter, from a JSON body, from a client. The typed
spelling exists and is opt-in rather than forced — the generated metamodel's
`Rel.Path()` and `attr.Name()` return plain strings that feed the same resolver
(`crud/decorators/specs/metamodel.go:164`, `:28`), so nothing forks: `WalkPath`
stays the single source of truth (`crud/relation.go:356`).

The price is that a hand-written path fails at request time, and [[D-021]] says
the magic must fail at build or start-up. The reference already draws that
contrast for relation paths and for aggregate field names
(`docs/modules/en/crud.md:183-190`, `:234-236`); what it does not say anywhere is
that the same is true of a `Where` or `OrderBy` field path, which is where a
consumer meets it first. And the build-time half is not universally available:
the generator expands a relation only when the target model is in the package
being generated and skips it silently otherwise
(`internal/codegen/render.go:222-224`), so a model layout spanning packages loses
the relation half of the check with nothing in the output saying why. That is a
sentence in the reference plus a line in the generator's output, not a redesign.

**The aggregate proposal, stated concretely enough to cost.** Two shapes, and
both have to carry their own refusals or they become the eleven dropped preload
options in a new package.

*Threshold.* `crud.Having(p)` sets `o.Agg.Having`, and `GtAgg`/`LtAgg`/`EqAgg`
build closed nodes over an `Aggregation` **value**, never over its alias string.
The alias must not be the argument: `"revenue"` is indistinguishable at the type
level from a model field named `Revenue`, so a typo'd alias would resolve against
the model rather than being refused, and the same term would mean different
things depending on whether `Agg` is set. Two refusals ship with it: a `Having`
in an option list with no `Agg` is a `SchemaError` at `Validate` time, never a
dropped option; and an aggregate comparison node refuses to render outside a
`HAVING` clause, so `crud.Where(crud.GtAgg(revenue, 1000))` cannot put `SUM(...)`
in a `WHERE`.

*Rank.* This one is a type change and round 1 hid it. `Order` cannot grow a
`Desc()` method — it already has an exported `Desc bool` field
(`crud/predicate.go:511-517`), which is why the existing modifiers are spelled
`WithNullsLast`/`WithNullsFirst`. So `Order` gains an `Agg *Aggregation` field
and `sortExpr` branches on it *before* the `cur.meta.Field` lookup
(`crud/predicate.go:537-546`), which gives the alias sort the same protection the
`Having` argument gets: an aggregate sort never enters the model namespace at
all. The constructor is `crud.ByAgg(revenue)` returning an `Order`, and `Desc`
stays a field.

Closing H-CRUD-12 (2) is the same edit's cheap half: validate `o.Sort` against
`Agg.GroupBy` and the alias set, and the 42803 that reaches a client today
becomes a refusal.

### What it must not break

- **[[D-011]] — and this file challenges it.** D-011's invariant is "upsert when
  the key is set", and its *What it forbids* says in as many words: "Do not split
  `Save` into `Insert` and `Update` 'for clarity'. The single method is the
  contract." Blocker 3 asks for exactly that split, so the challenge is stated
  here rather than left for the owner to discover. It is narrower than the
  prohibition: the JPA shape is right for a key the *database* owns, where "does
  the row exist" is a round trip the caller should not pay for. It is wrong for a
  key the *client* supplies, where the caller already knows which of the two they
  meant and the library does the other one silently — a create that overwrites
  (H-CRUD-03 (6)), a `PUT` that is last-write-wins on a model whose `version` tag
  says otherwise (H-CRUD-05 (6)), and a projected read-modify-write that zeroes
  the columns it never fetched (H-CRUD-09 (5)). D-011's own "Why one method"
  reasoning does not cover that case, because there is no round trip to avoid.
  The two ways out are a distinct verb or a flag on `Save` — which D-011 also
  forbids ("Do not add options to `Save` and imply they narrow anything"), though
  an insert-only flag narrows nothing. [[D-012]] leans on D-011 for `PUT`'s 404
  and moves with it. **If the owner keeps D-011 as written, what this file needs
  instead is one line in the reference saying that a client-supplied key makes
  every create an upsert** — because today nothing says it anywhere a consumer
  reads.
- [[D-030]] — a new verb on the seam is an obligation on every decorator. An
  `Insert` is not one method on one interface: it is a row in `coreVerbs`, an
  override on `security.gate`, and a test that will not compile until the reason
  is written down. That machinery is why the change would be safe, and also why
  it is not a one-line change.
- [[D-003]] — the AST is closed. A `Having` is new closed nodes over the
  aggregate values, never a `Raw` fragment with a different name.
- [[D-054]] — the closed AST gets one marshaller, `document` is on the
  `Predicate` interface so a node that forgets a wire spelling does not compile,
  and exactly three nodes are refused by name. A `Having` node must either gain a
  DSL spelling or extend that list to four; the alias sort needs the same
  decision, because `crud/query` compiles `sort` from a wire string and an alias
  sort therefore needs either a spelling or an explicit refusal. Either is a
  position; silence is not.
- [[D-004]] — `Where` ANDs. A reusable bundle, a `With`, a `Having`: none may
  weaken a predicate another layer added. H-CRUD-06 (3) records that this is a
  rule and not a type property, which is a note on the decision rather than a
  challenge to it.
- [[D-013]] — an unknown field is a rejection. An alias-aware resolver must still
  refuse a name that is neither a column nor an alias, and must not let an alias
  and a column trade places. The empty-`In` short-circuit (H-CRUD-17 (4)) is a
  small existing exception and should close with it.
- [[D-029]] — a summary carries every narrowing a row read carries. That is the
  reason `Having` belongs here and not in the caller's SQL.
- [[D-028]] — a cursor is the sort tuple and belongs to the sort it was made for.
  The fix in H-CRUD-08 is to mint fewer cursors, never to accept a token under a
  sort it does not match.
- [[D-060]] — a request may not choose how much comes back. The lever that is
  armed is `query.Config.AllowUnpaged`, closed by default; the clamp in
  `Resolved` is the one D-060 says was never armed, because `MaxLimit` defaults
  to zero. Do not open `AllowUnpaged` without changing that default — and note
  that arming the clamp is what turns `GetAll(ctx, crud.Unpaged())` into a silent
  truncation (H-CRUD-07 (7)), so the two have to move together.
- [[D-026]] — status **open**. `UpdateAll` and `DeleteAll` emit no `LIMIT`, and
  the gated versions inspect a set the write then exceeds. The `Unpaged()` shape
  of that mismatch is not in the decision and should be added to it.
- [[D-031]] — soft delete is a statement, not a decorator: declaring the delete
  behaviour is what declares the read behaviour. Nothing proposed here may let
  the two be declared apart.
- [[D-006]] — the preloader chunks at 900 for a reason. Whatever chunks `SaveAll`
  should say it is the same reason.
- [[D-024]] — status **open**. A `DISTINCT` must never be a no-op the caller
  cannot see, and today a bare `Distinct()` is exactly that. Nothing here may
  widen a `DISTINCT` projection to cover a sort, or add the key back.
- [[D-061]] — a wrapper forwards what it wraps. The README example has to change
  to `crud.Base`, not the other way round.
- [[D-019]] — a dialect difference is not observable. `Order.WithNullsLast()`
  renders on PostgreSQL only (`crud/predicate.go:597`) and `ForUpdate()` renders
  nothing on SQLite (`crud/dialect.go:164`). Both are documented and deliberate;
  both are also "accepted and then ignored", so nothing added here may join them.
- [[D-016]] / [[D-036]] — none of this may reach for a dependency. This also
  constrains H-CRUD-15's remedy: `crud` is stdlib-only and `port` imports `crud`,
  so the start-up complaint cannot be raised from where the walk lives.
- [[D-042]] — composite keys are left to the seam that would have to carry them.
  H-CRUD-02 does not propose changing that, only that the reference a consumer
  has open while modelling say it.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| The struct is the mapping, no registration | Exactly that: four of seven fields need no tag, three an option-only one. `Define` panics on eighteen refusals in `crud/meta.go` plus the relation, ID-type and update-DTO checks | none (crud) · see Sqlrepo.md for what a resource costs |
| A wrong mapping is never silent | True for the column checks; false for two — a `db` tag on a struct-shaped field and on an embedded struct are dropped without a word — and **not attempted for relations**, which resolve lazily and fail on the first request that crosses them | small (the two) · medium (relations) |
| Three states in one type — model, DTO, JSON, SQL | `crud.Opt[T]` does all four. The reference names three accessors that do not exist and omits the two that matter (`IsNull`, `MustGet`); `omitempty` instead of `omitzero` turns "leave it" into "clear it"; a plain `T` field's "always applied" is only in the README | none (code) · small (docs) |
| A query is a list of values you can build up | 22 `Option`s, 26 `Predicate`s, 4 `Order` constructors, 7 `Aggregation`s, 11 `sqlrepo.Setting`s — variadic and nil-tolerant at both levels. The one place the vocabulary is not flat is paging: four call-site options resolve against two declaration settings in `Resolved`, and two of the outcomes are silent | none (shape) · small (paging) |
| An option a call cannot honour is refused | Six of the eleven seam verbs drop options silently, with six different subsets and no document naming any of them | large |
| A reusable named filter | `func(...) crud.Option` works, and type-checks against every model in the service — the mismatch is an `UnknownFieldError` at request time. `specs.Specification[M]` is the typed shape and changes the call site with it | small |
| A filter that can be persisted and replayed | `MarshalPredicate` covers the predicate, refuses three nodes by name; the sort, preloads and projection do not survive | small |
| A field-name typo caught before the request runs | Runtime `UnknownFieldError` for a hand-written path; build-time only through the metamodel, which skips cross-package relations silently. An empty `In` never resolves the name at all | small · large if you never generate |
| A filter value taken from an optional field | `crud.Eq("Age", opt)` binds NULL and matches nothing. The predicate path is the one path that never calls `ElemValue` | large (it is silent) |
| Create a row and get back what was stored | One call, key filled, `generated` read back, `immutable` honoured — and a column `DEFAULT` never fires, with the documented remedy naming a hook that does not exist at this layer | small |
| Create a row that must not overwrite | No `Insert`: `Save` upserts whenever the key is set, so a client-supplied key silently overwrites — and [[D-011]] forbids the obvious fix | large |
| An optimistic lock on concurrent edits | A tag, `ErrStaleVersion` vs `ErrNotFound`, and five start-up refusals — covering `Update` and `UpdateAll` and **not** `Save` | small (docs) · large (the gap itself) |
| A page the frontend can render | `PaginatedResponse` + `MapPage`, overflow-safe, with a primary-key tiebreaker so page 2 does not repeat page 1. `Total` is not a total under `SkipTotal` or any cursor walk; the cap is off by default and, once on, silently truncates `GetAll(Unpaged())` | small |
| Cursor paging for a feed | Works on a NOT NULL sort. On a `crud.Opt` sort the server mints a token it then refuses; on a `sql.Null[T]` sort it accepts one and returns fewer rows than exist; through a relation, or under `UnstablePagination`, it mints nothing and says nothing | large |
| Relation filter without a join | `crud.Eq("Author.Name", …)` → correlated `EXISTS`. One argument | none |
| The list read that skips the big column | `Select` works and keeps the key and the join columns; saving that model then writes zeros over what it did not fetch | small (docs) · large (the upsert behind it) |
| A preload trimmed, narrowed, sorted or bounded | Predicate and sort are honoured; eleven other options are accepted and dropped — one of them a narrowing — and there is no per-parent limit at all | small (refuse them) · large (honour them) |
| A filter the vocabulary does not have | `crud.Raw`, binding properly and portably; column names unresolved, so a rename dies at request time | none (capability) · small (reviewability) |
| A dashboard summary you can rank and threshold | `GroupBy` + `Aggregate`; no `HAVING`, no ordering by an alias, a sort over an ungrouped column renders and the driver refuses it, and every group past the twentieth silently dropped | large |
| Delete one, delete many, soft-delete | Scope reaches both verbs and soft delete declares both halves; a missing row is `(0, nil)` with nothing saying the count is the only signal; `DeleteAll(ctx)` with no options takes the table | small (docs) · medium (the empty-filter shape) |
| A batch write | One statement, mixed batches refused — and no chunking at all, so the ceiling is a driver error at 3am; no read-back on MySQL | medium |
| A count and an existence check | Both carry the narrowing, `Distinct` counts what the list shows; `Exists` as a create pre-flight is check-then-act and nothing says so | small (docs) |
| One repository shared by every request | Almost certainly safe, claimed nowhere, with no concurrency test over the shared metadata; a `Define` after the first relation resolves is a silent no-op | small (a test and a sentence) |
| A jsonb blob or a text[] column | A `Valuer`/`Scanner` pair works and is the answer; nothing says so, and the failure without one is a field that reads back zero | small (docs) |
| Join someone else's transaction | One line, and one more to name the database — and naming the transaction instead of the database is a write that leaves the transaction and reports success | small (a refusal) |
| Reads on a replica | One call; every decision-read is on the primary except the one that decides retry-or-stop | small |
| Instrument the datasource | Three methods plus `UnwrapSource`; nothing checks you wrote the fourth, one of the three losses is silent, and the tier rules make the obvious complaint hard to place | small · medium (where it lives) |
| A rule above the repository | Every write is on the seam ([[D-030]] keeps it that way), order is stated, `crud.Base` forwards — and the README teaches the shape that does not | none (code) · small (docs) |
| Branch on failures | Seven sentinels and `errors.Is`; a client's bad input is a `SchemaError` with no `Is` and only a `Reason` string | small |
| A composite-key table as a resource | Refused at start-up; said in the probe's reference, not in the model reference | large |
| A fourth engine | The `Dialect` seam is genuinely open and defaults safely; nothing says a fourth engine loses `ErrConflict` classification | none (code) · small (docs) |

**Overall:** the ordinary Tuesday is short. Declare, filter, sort, page, preload,
patch, join a transaction — each is one call with the arguments it needs and
nothing else, and the parts compose without a builder or an explicit type
parameter anywhere. Customising is genuinely additive for reads: nearly
everything a consumer reaches for is one more element in the same option list.
The write half is where the shape thins out — `Save` is one method doing two jobs
that need different answers under contention, and every finding in H-CRUD-03,
H-CRUD-05 and H-CRUD-09 traces back to that one fact, which is why they are one
blocker and not three. Where the code stops feeling like the ideal is not
verbosity but silence: eleven of the findings below are things the library
accepts and then ignores, or mints and then refuses, or counts and then
truncates. That is the opposite of the failure mode this repository's own
decisions are built around, and it is the part worth fixing before the tag.

## Release blockers found here

Rows 1–14 are behaviour: wrong data, or a wrong answer a consumer cannot see.
Rows 15–22 are sharp edges. Rows 23–26 are documentation, and one editing pass
closes all four.

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A cursor sort over a `sql.Null[T]`/`sql.NullTime` column is minted **and accepted**, and silently returns a page short of rows (`crud/meta.go:503` vs `crud/cursor.go:124`) | blocker | 200, well-formed, fewer rows than exist, no error anywhere — and it is the exact model shape [[UC-010]] promises to adopt. It is also why the obvious three-line fix for #2 does not close it |
| 2 | Every paged list over a nullable sort column emits a `nextCursor` its own next request refuses (`crud/sqlrepo/repository.go:238` vs `crud/cursor.go:124`) | blocker | `setCursors` runs on both branches of `Get`, so this reaches consumers who never opted into cursor paging and do not know the field is there. A tag freezes the wire behaviour. Must ship with the "no cursor by design" signal or it becomes #10 |
| 3 | `Save` is an upsert whenever the key is set, with no version check and no way to say "insert only" (`crud/sqlrepo/repository.go:623`) | blocker | Three symptoms, one fix: a create over a client-supplied key overwrites the row that was there (H-CRUD-03 (6)); a `PUT` is last-write-wins on a model whose `version` tag says otherwise (H-CRUD-05 (6)); a projected read-modify-write zeroes the columns it never fetched (H-CRUD-09 (5)). **Closing it challenges [[D-011]] and is [[D-030]] work.** Overlaps H-SQLREPO-05 and Sqlrepo blockers 2 and 7, and Index gaps 2 and 17 — one decision, several rows |
| 4 | A filter value held in a `crud.Opt` binds NULL and matches nothing (`crud/predicate.go:154-157`, `crud/opt.go:105`) | serious | The predicate path is the only path that never calls `ElemValue`, and `crud.Eq("Age", dto.Age)` is the line [[D-002]] leads a consumer to write. Silently empty result, no error |
| 5 | Eleven options inside `PreloadWhere` are accepted and dropped, one of them a narrowing (`crud/preload.go:128`, `:268`, `:299`) | serious | `NarrowRelations` on a preload constrains nothing, a nested `Preload` does nothing, `Select` returns the big column anyway. Pagination is refused loudly eighty-three lines above; nothing else is |
| 6 | Six seam verbs silently drop options, with six different subsets and no document naming any of them (H-CRUD-06's table) | serious | `UpdateAll`/`DeleteAll` take a `Limit` and emit none ([[D-026]], open); `Aggregate` drops seven. A filtered write does more than it was asked and reports the count as if that were the answer |
| 7 | An aggregate read is silently truncated to the repository's default page size (`crud/sqlrepo/repository.go:1051`, `crud/sqlrepo/blueprint.go:26`) | serious | A dashboard over 21 statuses shows 20, with no total, no flag and no error. **= Sqlrepo blocker 8, one fix** — kept here because `crud.Unpaged()` is the escape and it is this package's option |
| 8 | `missedRow`'s existence check goes to the replica (`crud/sqlrepo/repository.go:817`) | serious | It decides retry-or-stop after an optimistic-lock miss. On a lagging replica a deleted row retries forever and a live conflict reports as vanished. [[D-032]] names this class explicitly. **= Sqlrepo blocker 3, one fix** |
| 9 | An aggregate sort over a real column that is not in the `GROUP BY` is rendered straight through (`crud/aggregate.go:87-140` never sees `o.Sort`; `repository.go:1048`) | serious | PostgreSQL answers 42803 — a 500 for a statement this package built, on the one verb whose whole justification is that hand-written SQL is worse |
| 10 | A cursor sort through a relation, or under `sqlrepo.UnstablePagination()`, returns no `nextCursor` and says nothing (`crud/sqlrepo/repository.go:248`, `crud/cursor.go:19-22`) | serious | A capability drops out with no signal, and it is the half of #2 that must ship alongside it or #2's fix silently breaks working feeds |
| 11 | `GetAll(ctx, crud.Unpaged())` returns exactly `MaxLimit` rows while `GetAll(ctx)` returns every row (`crud/sqlrepo/repository.go:271-284`) | serious | The most emphatic way to ask for everything is the one way to get less, silently — and `security.gate` inspects victims through `GetAll`, so a gated `DeleteAll` can inspect 200 rows and delete every match ([[D-026]], open) |
| 12 | `WithExecutorFor` keyed on a transaction handle matches no repository (`crud/executor.go:335`) | serious | The write leaves the transaction and reports success, from a call identical at the call site to the right one. H-SQLREPO-12 is ✅ and hands it here (`Sqlrepo.md:522-529`); this sweep claims it. Index gap 18 |
| 13 | `SaveAll` and `Delete(ids...)` build one statement per call and never chunk (`crud/sqlrepo/repository.go:1124-1176`) | serious | The only bulk verbs have an undocumented row ceiling below any real import, and crossing it is a driver error naming nothing about vv. **= Sqlrepo blocker 4**; listed here because the ceiling is invisible from `crud/repo.go:43`, where the call is chosen |
| 14 | A `db:"…"` tag on a struct-shaped field, or on an embedded struct, is dropped in silence (`crud/meta.go:375`, `:347`) | serious | The consumer asked for a column and got none. Scoped to a hand-written mixin — an adopted ORM entity carries no `db` tag and still flattens, which is what [[UC-010]] guarantee 4 relies on |
| 15 | `Total` is the page length on every cursor walk, because `After`/`Before` set `NoTotal` implicitly (`crud/options.go:121`, `:131`) | sharp edge | A pager rendered from that field prints "of 0" on every infinite-scroll endpoint, and `crud/page.go:14`'s doc comment is stale about when cursors appear at all |
| 16 | The page cap is off by default: `MaxLimit(0)` means no cap, so `Unpaged()` returns the whole table on a stock `Define` (`crud/sqlrepo/blueprint.go:54`) | sharp edge | [[D-060]] already records this and closed the wire door; the in-process door is open and the clamp at `crud/options.go:241` reads like a defence that is standing. Fixing it arms #11 |
| 17 | A relation whose join field does not exist starts the process and fails on the first request that crosses it (`crud/relation.go:94`, `:119-120`) | sharp edge | The start-up promise this library sells covers columns and not edges, and nothing says so. Same mechanism: a `Define` after that relation resolves is a silent no-op (`crud/relation.go:95-108`) |
| 18 | `DeleteAll(ctx)` with no options and no `Scope` takes the table; empty `In` is `1 = 0` and empty `NotIn` is `1 = 1` (`crud/sqlrepo/repository.go:903-918`, `crud/predicate.go:201-210`) | sharp edge | Three spellings of "no filter", two of them destructive, none documented. The empty `In` also skips field resolution, which is the one hole in [[D-013]] |
| 19 | `crud.Distinct()` with no projection deduplicates nothing and says nothing ([[D-024]], status open) | sharp edge | The caller sets a flag, the statement carries the keyword, every row still differs by primary key. The decision that names it is unresolved |
| 20 | `SchemaError` and `UnknownFieldError` have no `Is`, and `SchemaError` is now a request-time client error (`crud/errors.go:41`, `:52`) | sharp edge | The two failures a handler most needs to turn into a 400 can only be reached by type assertion and a `Reason` string. Its doc comment still claims it is start-up only |
| 21 | The cursor's nullable-column refusal (`crud/cursor.go:126`) has no test anywhere | sharp edge | An unpinned refusal can regress into the silent wrong answer it was written to prevent ([[D-020]]) |
| 22 | Composite primary keys are refused at start-up; the model reference does not say so | sharp edge | Deliberate ([[D-021]], [[D-042]]) and stated at `docs/modules/en/probe.md:197-199`, which is not the page open while a join table's key is being chosen |
| 23 | **Docs.** `docs/modules/en/crud.md:84`, `:85`, `:87` and the same lines in `ru/crud.md` document `Defined()`, `Valid()` and `OrZero()`; the methods are `IsDefined()`, `IsSet()` and `OrElse()`, and `IsNull()`/`MustGet()` are missing entirely | sharp edge | Thirty seconds and a compile error, no data at risk — but it is the first place an `Opt` accessor is looked up, and `IsNull` is the accessor the type's whole three-state design exists for |
| 24 | **Docs.** The README's decorator example embeds `crud.Core` with no `Next()` (`README.md:501`) | sharp edge | It is the snippet consumers copy, it contradicts `docs/modules/en/crud.md:317`, and a probe above such a decorator panics at start-up |
| 25 | **Docs.** A column `DEFAULT` never fires, and the remedy the references give names a `BeforeSave` hook that exists only on the transports | sharp edge | `README.md:1545-1551` is absent from the model reference; `docs/modules/en/sqlrepo.md:283` (Sqlrepo blocker 17) names a seam a background job cannot reach. One edit, two files |
| 26 | **Docs.** Nothing states that a bound repository is safe to share across goroutines, that a `jsonb` column needs a `Valuer`/`Scanner`, or that a fourth dialect loses `ErrConflict` classification | sharp edge | Three first-week questions with no answer in the reference. The concurrency one also has no test, over three package-level caches and two `sync.Once`s per relation |

## Contested

- **Blockers 7, 8 and 13 are also Sqlrepo blockers 8, 3 and 4.** Two reviewers
  said the owner would count them twice. Kept, and each row now names the sibling
  and says "one fix", the way H-CRUD-14 cites H-SQLREPO-09 in its body. They stay
  because each is reached through a `crud` decision a reader of this file needs —
  the `Unpaged()` escape, `PrimaryOnly()` on a decision-read, and a seam method
  whose doc comment gives no ceiling. Dropping them would make this sweep's read
  path look correct on its own.
- **The cursor mint-side defect belongs here, not in the sqlrepo sweep.** A
  reviewer noted that `setCursors` lives in `crud/sqlrepo/repository.go`. Kept:
  the defect *is* the disagreement between the mint side and `crud/cursor.go`,
  and only a document owning both halves can see it — split across two sweeps it
  is two correct-looking paragraphs. The sibling's H-SQLREPO-04
  (`Sqlrepo.md:163-189`) is ✅, and its must-hold 4 — a cursor refused under a
  different sort — is the one cursor guarantee this file also finds holding; what
  blockers 1, 2 and 10 undercut is that case's overall ✅, not that must-hold.
  Round 1 cited the wrong case and the wrong must-hold here; the correction is
  the reviewer's.
- **The page cap stays in this file, stated as a negative** (blocker 16). A
  reviewer argued the ceiling is `sqlrepo.MaxLimit`. Kept, because a reader of the
  `crud` sweep needs to know what `crud.Unpaged()` does on a stock repository, and
  the clamp whose doc comment promises the defence is `crud/options.go:241`.
  Blocker 11 is the same argument and now carries it explicitly.
- **H-CRUD-06 (3) keeps the claim that no option can weaken a predicate, in the
  weaker wording.** A reviewer was right that `Options` is an exported struct and
  `Option` a function over it, so the strong form is false; the case now says
  which of the two it is. What is *not* conceded is that this makes the design
  wrong: the export exists so decorators can inspect the shape, and an opaque
  `Options` would cost the gate its ability to read what it is narrowing. A rule
  held by a decision doc plus a test is how the rest of this repository works too.
- **H-CRUD-12 (4) is restated and downgraded.** Round 1 marked "a summary can be
  cut to the top N" as working because `Limit` is applied. A reviewer called that
  the one place the file flattered the code, and it was: ten arbitrary groups is
  not a top ten. The must-hold now reads "ranked and cut" and fails with (5) and
  (6).
- **The headline no longer carries the `Opt` accessor typo.** A reviewer pointed
  out that a compile error is the loudest possible failure and the opposite of
  "quietly wrong". Moved to its own clause and to blocker 23, under a docs
  heading, with the cost stated.

## Edge cases

### E-CRUD-01 — Hostile page controls cannot form invalid SQL
**Shape:** adversarial input
**Setup:** A request adapter forwards a negative page, limit or offset, and a saved-search helper supplies the same paging knob twice.
**What the consumer does:** They replay the saved options and append the request options without sanitising either list themselves.
**What must happen:** No negative offset reaches a statement, non-positive page and limit values resolve predictably, and a repeated value has one documented winner.
**Today:** ✅ handled
**Evidence:** `crud/options.go:56-73` applies options left to right; `crud/options.go:241-276` normalises non-positive paging values, clamps the limit and discards a negative offset. `crud/edge_test.go:430-477` pins last-value-wins and the negative-offset control.
**Blast radius:** none

### E-CRUD-02 — A forged cursor is refused before it becomes a filter
**Shape:** adversarial input
**Setup:** A client alters a copied cursor so it is not base64, is not its expected JSON shape, has unequal field/value counts, names another sort, or contains a value that cannot fit the sorted column.
**What the consumer does:** They send that string back as the next-page cursor.
**What must happen:** The caller gets an error before a cursor predicate or bind list is produced; the server must never reinterpret a position under another sort.
**Today:** 🟡 partial
**Evidence:** `crud/cursor.go:57-91` rejects malformed encodings, shape and sort mismatches, and values that cannot decode into the target column type; `crud/cursor.go:108-133` performs those checks before it builds comparison branches. No `CursorPredicate` test was found under `crud/`.
**Blast radius:** confusing error

### E-CRUD-03 — A reused optional value cannot retain a rejected request
**Shape:** partial failure
**Setup:** A decoder or scanner reuses an `Opt[int]` that held a prior request's value, then receives `null` or a value the element type cannot accept.
**What the consumer does:** They decode or scan into the reused DTO field rather than allocating another wrapper themselves.
**What must happen:** `null` replaces the old value, while an invalid body or scan reports an error and leaves the prior state intact; neither path may leave a half-decoded value.
**Today:** ✅ handled
**Evidence:** `crud/opt.go:131-147` assigns only after a successful scan, except that SQL NULL deliberately becomes null; `crud/opt.go:164-175` unmarshals into a temporary before assigning. Pinned by `crud/opt_edge_test.go:50-75` and `crud/opt_edge_test.go:185-274`.
**Blast radius:** none

### E-CRUD-04 — A hand-written adapter gives a schema the wrong model
**Shape:** misuse
**Setup:** An adapter hands `Schema.Pointers`, `Values`, `ID`, `HasID` or `SetID` a value, a nil pointer, or a pointer to another model.
**What the consumer does:** They implement the executor seam and make one reflective call with a value whose type is not the schema's model.
**What must happen:** The call reports a schema error instead of panicking or writing through an unrelated pointer.
**Today:** ✅ handled
**Evidence:** `crud/access.go:9-14` checks that the value is a non-nil pointer to the schema type; `crud/access.go:17-81` routes every listed accessor through that check. `crud/access_test.go:151-183` pins all four bad shapes across all five accessors.
**Blast radius:** crash

### E-CRUD-05 — An empty boolean group has an honest meaning
**Shape:** boundary
**Setup:** A search builder collects no terms, or only nil terms, and passes its `And`, `Or` or `Not` result straight into `Where`.
**What the consumer does:** They use one generic predicate builder for an empty form and for a populated form.
**What must happen:** The result has defined Boolean semantics and valid SQL: empty AND is true, empty OR and `Not(nil)` are false, and no empty parenthesis reaches the driver.
**Today:** ✅ handled
**Evidence:** `crud/predicate.go:290-347` drops nil children, flattens groups and renders the three identity cases as constants. `crud/predicate_test.go:112-143` pins empty, nested and nil-containing groups.
**Blast radius:** none

### E-CRUD-06 — A deep preload request cannot multiply work without a limit
**Shape:** adversarial input
**Setup:** A client asks for six self-relation hops, or a resource deliberately lowers its preload depth below the requested path.
**What the consumer does:** They pass the requested preload paths through to the core preloader.
**What must happen:** The path is refused before the first related-query statement, with the violated depth named; it must not turn one list request into unbounded follow-up queries.
**Today:** ✅ handled
**Evidence:** `crud/preload.go:11-14` sets the default cap; `crud/preload.go:70-102` rejects a path beyond it; `crud/preload.go:113-130` validates the tree before entering the query loop. `crud/preload_test.go:353-375` pins the default and a tightened cap, including the no-statement control.
**Blast radius:** none

### E-CRUD-07 — A thousand selected IDs have a framework-level limit
**Shape:** scale
**Setup:** An export, cleanup job or manually assembled filter passes thousands of IDs to `In` or `InAny`.
**What the consumer does:** They expect the same closed predicate API to either handle the list in safe chunks or reject it with a framework error.
**What must happen:** The root API enforces a caller-visible bind budget or refuses before asking a driver to parse an oversized statement.
**Today:** ❌ wrong or unhandled
**Evidence:** `crud/predicate.go:201-225` emits one bind marker for every value and contains no limit, chunking or refusal path. No large-list test was found under `crud/`.
**Blast radius:** confusing error

### E-CRUD-08 — An organisational hierarchy may point back to itself
**Shape:** degenerate declaration
**Setup:** An employee model has `Manager *Employee`, and a request filters or sorts through `Manager.Manager.Name` under a relation scope.
**What the consumer does:** They model the normal self-referential hierarchy without introducing a duplicate model only to break the cycle.
**What must happen:** Resolving the target and walking repeated hops completes, and the scope lands on the hop it names rather than recursing indefinitely or leaking to another hop.
**Today:** ✅ handled
**Evidence:** `crud/relation.go:93-109` resolves a target once, while `crud/relation.go:300-334` separates default resolution so the two lazy steps cannot deadlock. `crud/relation_test.go:261-275` pins the self target; `crud/predicate_test.go:328-364` pins a two-hop self path and its scopes.
**Blast radius:** none

### E-CRUD-09 — Concurrent first callers receive one metadata identity
**Shape:** concurrency
**Setup:** Two request paths call `SchemaOf` for the same model before either has populated the process cache.
**What the consumer does:** They initialise repositories lazily in parallel and rely on the documented per-type cache rather than serialising the first call themselves.
**What must happen:** Both callers receive the one cached schema and its field identities; a cache must not split its first result by caller.
**Today:** ❌ wrong or unhandled
**Evidence:** `crud/meta.go:195-205` and `crud/meta.go:209-217` perform a load, build and store without an atomic get-or-build step, so simultaneous misses can each return their own built schema. `crud/schema_edge_test.go:420-438` proves only the sequential cache case; no concurrent first-schema test was found under `crud/`.
**Blast radius:** confusing error

### E-CRUD-10 — Concurrent first relation crossings resolve one join
**Shape:** concurrency
**Setup:** Two first requests cross the same relation at the same time.
**What the consumer does:** They share one bound repository across requests and hit a relation only after traffic starts.
**What must happen:** Both calls see the same resolved local and remote fields, without a race on the process-shared relation.
**Today:** ✅ handled
**Evidence:** `crud/relation.go:75-91` gives target and default resolution separate `sync.Once` guards; `crud/relation.go:300-317` writes the default fields only inside the second guard. `crud/relation_test.go:474-522` runs 32 concurrent resolutions and checks the resolved field names agree.
**Blast radius:** none

### E-CRUD-11 — A decorator chain that loops cannot hang service start-up
**Shape:** misuse
**Setup:** A hand-written decorator's `Next` method accidentally returns the decorator itself.
**What the consumer does:** They wire a probe or another feature that asks the chain for its source.
**What must happen:** The source lookup ends with no source rather than following the cycle forever or inventing one below the bad layer.
**Today:** ✅ handled
**Evidence:** `crud/executor.go:178-208` caps a core-chain walk at 64 layers and returns failure when it cannot reach a source. `crud/basenext_test.go:84-106` pins the self-loop control with a timeout and confirms no source is fabricated.
**Blast radius:** none

### E-CRUD-12 — A cancelled owned transaction still has a defined cleanup outcome
**Shape:** partial failure
**Setup:** The context is cancelled after the transaction begins and the callback returns that cancellation error.
**What the consumer does:** They use `InTx` around work subject to an HTTP deadline and rely on the helper to finish the transaction lifecycle.
**What must happen:** The original failure is returned and the transaction is cleaned up predictably even when the request context is no longer usable.
**Today:** ❓ unverified
**Evidence:** `crud/executor.go:503-527` passes the original context to `Begin`, `Rollback` and `Commit`, and joins a rollback error with the callback error. No cancellation, rollback-failure or commit-failure test was found under `crud/`.
**Blast radius:** confusing error

## Edge verdict

The worst new edge is a manually assembled large ID filter: the core predicate
writer accepts every value and leaves the first useful limit to the driver. The
root package is otherwise closed against malformed paging, invalid reflective
access, malformed preload depth, self-relations and a cyclic decorator chain;
the strongest of those claims have focused controls. Its first-use metadata cache
does not make one schema identity atomic, and the transaction-cleanup claim has
no cancellation or rollback-failure proof. These are smaller than the existing
silent data findings, but they make lazy initialisation and operational failure
less predictable than the short call site suggests.

## Release blockers found here (edge)
| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A manual `In` list has no caller-visible bind budget, batching or refusal. | serious | A normal export or cleanup filter reaches an adapter-specific oversized-statement failure after the root API accepted every ID; the caller cannot derive a safe ceiling from this package. |
