# crud/sqlrepo — one declaration next to the model, and the whole CRUD surface over any driver

**Covers:** `github.com/frostgrove/vv/crud/sqlrepo`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — the write half of this module does not honour the declaration a consumer reads it as: the permanent scope reaches no write statement, and a second table or relation scope can silently replace the first and leak rows. A save over a tombstone's key resurrects the row, a keyed `Save` overwrites every column from a half-filled model and bypasses optimistic-lock refusal, and MySQL can apply that upsert to a row selected by a different unique key. Relations on a model are written nowhere and refused nothing, and the batch verb has a row ceiling below any real import. Two read paths truncate silently and report the truncation as the whole answer.

## What a consumer is actually trying to do

Somebody has a table and a Go struct and does not want to write the twelve
statements between them a fourth time. They arrive with `users`, `articles`,
`orders`, an HTTP handler that already exists, and a connection pool they opened
themselves and intend to keep. What they want is for the struct to be the
declaration — tag the key, tag the timestamp the database owns, say the table
name — and for reading, listing, patching, deleting and upserting to be there
afterwards without a repository file they maintain by hand.

The second thing they want is for a `PATCH` to mean what a `PATCH` means. A form
that submits three of eight fields must write three columns. A field sent as
`null` must clear the column, and a field left out must not. Nearly every
hand-written handler collapses those two, and the bug reads as data loss weeks
later, in a support ticket about a phone number that vanished.

The third thing is narrowing, and it is the one they will most confidently get
wrong. "This repository only ever sees this tenant's rows" and "this repository
never sees deleted rows" both sound like properties of the repository. They
arrive expecting to say it once and have it be true of everything the repository
does — reads, deletes, patches and creates alike — because that is what "only
ever" means in English.

Then the second month arrives and the shape of the work changes. The object they
loaded goes back with three fields set on it. An order carries its lines and both
have to land. A list screen shows the customer's country, so the filter and the
sort have to hop a relation. One screen wants three of a table's forty columns
because the fortieth is a megabyte of JSON. One report cannot be written in any
query language the library speaks, and the question becomes whether leaving is
cheap or expensive.

Then it gets less tidy still. Two people edit the same record. A nightly job
imports twenty thousand rows from a supplier file. A tenant is deactivated, which
means one filtered write and not eight thousand round trips. A dashboard needs
counts by status. An admin tool needs to see the rows the public API hides, and
sometimes to bring one back. Somebody adds a read replica, and somebody else adds
a second database for events. Two requests deadlock. Every one of those is a
place where "it worked in development" and "it is correct" come apart.

Underneath all of it is one constraint they will not trade away: the connection
is theirs. The transaction is theirs. A service method that already opens a
transaction with an ORM has to be able to hand it over, and a repository must not
open a second one behind its back. That is the difference between a library they
can adopt on a Tuesday and a framework they would have to rebuild the service
around.

## Happy cases

### H-SQLREPO-01 — The first hour: a struct becomes a repository
**Who:** a backend engineer adding a `users` table to a service that already runs
**Wants:** working reads and writes without a hand-maintained repository file or a base class
**Story:** They tag the struct, generate the update DTO, call `Define` and
`Bind`, and use it. They mistype something and want to know at start-up, not on
the first request in production.
**Must hold:**
1. Nothing in the declaration is a per-column artefact a human edits by hand; the struct tags and one generate directive are the whole input.
2. A declaration that cannot hold together stops the process and names the field. *(Owned by `crud` — pointer only, not re-evidenced here.)*
3. Every setting in the declaration is checked the same way, so a typo in a `Scope` or a `DefaultSort` is not a 500 on the first list request.
4. What is a column and what is not is visible in the struct, so a Go-only field does not silently become one.
5. A field the model has and the table does not is caught before traffic.
6. The same declaration can be bound twice — to a second database, to a test recorder — without redeclaring.
7. Nothing in the declaration mentions a driver, so swapping PostgreSQL for MySQL is one line at the binding.
8. A table whose key is two columns is either supported or refused in words the author can act on. *(Owned by `crud`.)*
**Today:** 🟡 partial
**Evidence:** 6 and 7 hold: `blueprint.go:246` (`Bind` takes a source plus
middleware) and the same blueprint bound to `crudtest.Postgres()` throughout
`crud/sqlrepo/*_test.go`. 2 and 8 hold and belong to `crud`; the pointers are
`crud/meta.go`, `crud/access.go` and `crud/update.go`, and the `crud` sweep
carries the evidence. Pinned here at the seam by
`TestBadDeclarationsAreRefusedAndSayWhy` (`blueprint_edge_test.go:47`) and
`TestBadDeclarationsPanicEarly` (`repository_test.go:599`).
Point 1 holds for the ordinary case and **fails as an absolute** the moment a
column has to be kept out of the update DTO: the spelling is `-readonly
DeletedAt` in the `go:generate` line (`cmd/vv/main.go:22-24`), which is a
per-column hand edit in the declaration. It is the right place for it, and it is
still a per-column edit — H-SQLREPO-08 point 7 depends on somebody remembering to
write it.
Point 3 **fails**, and it fails for the two settings most likely to carry a typo.
`TryDefine` (`blueprint.go:158-196`) runs `NewMeta`, `CheckID`, `PlanFor`,
`resolveSoftDelete` and `resolveRelationScopes` and nothing else; `Scope`
(`blueprint.go:85`) and `DefaultSort` store what they are handed without looking
at it. `sqlrepo.Scope(crud.Eq("Achived", false))` declares cleanly and fails as a
`crud.UnknownFieldError` on the first query — `TestAnUnknownDefaultSortIsRefusedBeforeTheQueryIsSent`
(`blueprint_edge_test.go:215`) pins that no statement is sent, which is not the
same as pinning that the process refused to start. `SoftDelete` and
`RelationScope` *are* validated (`blueprint.go:202-237`), so the two halves of
the same settings list behave differently and nothing says so.
Point 4 **fails**: an exported field with no `db` tag and a non-struct type
becomes a column named `snake(FieldName)` (`crud/meta.go:395-398`). Mapping is
opt-out, not opt-in, so adding a computed display value or an unsaved password to
the struct puts a nonexistent column into every statement for that table until
somebody finds `db:"-"`.
Point 5 has nothing at all — see H-SQLREPO-17.
**If not ready:** For point 3, walk `bp.set.scope` and `bp.set.defaultSort`
against the schema the way `resolveRelationScopes` walks paths — one function,
and it turns a production 500 into a start-up panic. For point 4 the answer is
documentation, not code: opt-out mapping is what makes an ent or gorm entity work
untouched, and reversing it now would break every consumer.

### H-SQLREPO-02 — Create a row and get back what the database owns
**Who:** the same engineer, on the create endpoint
**Wants:** to insert and hold a model that describes the row, keys and timestamps included
**Story:** They build a `User` from the request body, save it, and serialise the
result straight back. They expect the generated key and `created_at` to be
filled. They also expect the column `DEFAULT` they wrote in the migration to fire.
**Must hold:**
1. A model with an unset generated key inserts, and the key is in the model when the call returns.
2. Columns the database computes come back filled, on every engine — including the ones without `RETURNING`.
3. A model whose key the application owns and forgot to set is refused rather than written as a zero.
4. The response is the same document on PostgreSQL and on MySQL.
5. A column `DEFAULT` in the migration does something, or the author is told plainly that it does not — and told something they can act on from wherever they call `Save`.
**Today:** 🟡 partial
**Evidence:** 1–4 hold. `crud/sqlrepo/repository.go:609-624` branches on the key,
`:636-660` runs `RETURNING` or `LastInsertId`, and `:667` re-reads
unconditionally so the model describes the row on every dialect — the reasoning
is in [[D-011]] and the divergence is pinned by
`TestSaveLeavesTheCallerHoldingTheStoredRowOnEveryEngine`
(`test/integration/dialect_edge_test.go:205`).
Point 5 is the documented surprise plus an undocumented one. `crud/meta.go:285-299`
puts every non-`generated` column in the insert list, so a `DEFAULT` only fires
for a column tagged `generated`; that much is written up in
`docs/modules/en/sqlrepo.md` under "A column DEFAULT does not fire". **The remedy
that write-up offers does not exist at this layer.** It says to "fill it in a
`BeforeSave` hook", and `BeforeSave` is a transport option only —
`crud/http/crudnet/options.go:101`, `crud/http/crudgin/options.go:101`,
`crud/http/crudfiber/options.go:99`, `crud/rpc/crudgrpc/options.go`. `grep -rn
BeforeSave` finds nothing under `crud/sqlrepo/` or `crud/*.go`, so a service
method or a background job calling `users.Save` has no seam but a
`crud.Middleware`. The module reference is wrong and should say so.
The sharp edge nobody has written down: an `auto` key that is *not* an integer.
`crud/meta.go:432` infers `auto` only for integer kinds, so this shape needs an
explicit `db:"id,pk,auto"` on a `uuid` plus an engine-side default —
`gen_random_uuid()` on PostgreSQL, `DEFAULT (UUID())` on MySQL 8.0.13+ or a
trigger below that. On PostgreSQL it works. On MySQL `LastInsertId` is 0
(`repository.go:656` skips `SetID` for exactly that), the model keeps its zero
key, and the read-back at `:667` then answers `ErrNotFound` for a row that was
inserted. `test/integration/uuid_test.go:163` only exercises caller-assigned
UUIDs, so nothing catches it.
**If not ready:** Today the author tags every server-owned column `generated` and
assigns UUIDs in Go. Fix the module reference to name `crud.Middleware` as the
repository-level seam. Closing the MySQL case is a refusal at `Bind` time — a
non-integer `auto` key on a dialect without `RETURNING` cannot be read back, and
saying so at start-up costs one check.

### H-SQLREPO-03 — A form submits three of eight fields
**Who:** an engineer wiring a settings page
**Wants:** a `PATCH` that writes what was sent and nothing else
**Story:** The client sends `{"name":"Anna"}`. Later it sends `{"age":null}` to
clear a field. Later still it re-sends a value the row already holds, and the
`updated_at` trigger must not fire for that.
**Must hold:**
1. A field the body omits is not in the `SET` list.
2. A field sent as `null` writes SQL `NULL`, and is distinguishable from omission by the stored row.
3. A field whose value already matches produces no `SET` entry.
4. A body defining nothing issues no statement and still succeeds.
5. The returned model is the row as it now stands, including anything a trigger changed.
6. Patching a row that is gone is a refusal, never a success carrying a fabricated model.
**Today:** ✅ ready
**Evidence:** `crud/sqlrepo/repository.go:698` loads, `:725` diffs, `:729-730`
returns the loaded row when nothing changed. The re-read at `:793` exists because
patching the loaded row in memory used to report success for a row deleted
between the load and the write — the comment at `:776-780` names it. Pinned by
`TestUpdateWritesOnlyChangedFields`, `TestUpdateWithNothingToDoSkipsTheWrite`,
`TestUpdateDistinguishesUndefinedFromNull` and
`TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect` in
`crud/sqlrepo/repository_test.go:323-462`, and by [[UC-003]] end to end.
[[D-010]] holds the reasoning.
**If not ready:** n/a. This is the case the module is best at, and it is the only
verb in it where the narrowing, the diff and the read-back all agree.

### H-SQLREPO-04 — The list screen
**Who:** an engineer building an admin table with a search box and column sorting
**Wants:** filter, sort, page and total in one call, and a stable page as rows are inserted
**Story:** The screen asks for page 2 of active users in a tenant, sorted by
creation date, with a search term in the box. Later the mobile client asks for
the same list by cursor, because offset paging shows it the same row twice.
Later still, somebody adds an export button.
**Must hold:**
1. One call returns the rows, the total and the page count — and the caller can find out what the total costs before a table gets big.
2. A page size a client asks for is capped by the repository, including when the client asks to be unpaged — **and the response says it was capped.**
3. Two rows with the same sort value do not swap between pages.
4. A cursor made for one sort is refused under another rather than compared against whichever columns line up.
5. An unknown field name in a filter or sort is refused, never dropped.
6. The search box matches the same rows on both engines the library supports.
7. "Give me everything" means the same thing whichever verb spells it.
**Today:** 🟡 partial
**Evidence:** 1, 3, 4 and 5 hold. `repository.go:159` (`Get`), `:540` (`sortOf`,
appending the key tiebreaker), `:400` (`cursorWhere`, which refuses a sort that
does not end in the key). Point 4 is `crud/cursor.go:69-78` ("this cursor was made
for a different sort order"), pinned by `TestACursorIsRefusedUnderADifferentSort`
(`test/integration/cursor_test.go:238`). Unknown fields:
`TestUnknownFieldIsAnError` (`repository_test.go:527`), [[D-013]]. The half of
point 1 about cost is answered by `crud.SkipTotal()` (`crud/options.go:184`),
which drops the `COUNT` and keeps an honest `HasNext` from a one-extra-row probe
(`repository.go:173-206`), pinned by
`TestSkipTotalReportsWhatWasFetchedAndNotTheOffset` (`paging_edge_test.go:104`).
It is not mentioned anywhere a reader of this page would find it, so a consumer
finishes the paging documentation believing the total is free. On a fifty-million
row table with a filter, the `COUNT` is the request.
Point 2 **fails on its second half, and it is the same defect this file rates
serious for `Aggregate`.** The cap holds: `crud/options.go:238-247` returns
`maxLimit` for an unpaged request and `TestMaxLimitSurvivesEveryWayAPageCanBeAskedFor`
(`paging_edge_test.go:64-86`) pins that a `LIMIT` is emitted. The response then
lies about it. With `MaxLimit(50)` and 5000 matching rows,
`Get(ctx, crud.Unpaged())` takes the `case o.Unpaged` branch at
`repository.go:217-218` and sets `total = int64(offset + len(items))` — 50. From
there `crud/page.go:33` computes `TotalPages = 1` and `:38` computes `HasNext =
false`. The caller receives a truncated page reporting itself as the whole answer,
with no field that could say otherwise. The pinning test asserts only
`strings.Contains(rec.Last().SQL, " LIMIT ")`; it never looks at the response.
This is reachable from the wire — `?unpaged=true` or its alias `?all=true`,
gated by `query.Config{AllowUnpaged: true}` (`crud/query/compile.go:387-392`),
which is exactly the flag an export endpoint turns on.
Point 6 **fails, and it is the case's own story.** `crud.Contains("Name", q)`
renders a plain `LIKE '%…%'` (`crud/predicate.go:436` → `:425` → the render at
`:255-274`), which is case-*sensitive* on PostgreSQL and case-*insensitive* under
MySQL's default `utf8mb4_0900_ai_ci`. So `?q=anna` finds "Anna" on one engine and
not on the other, in a module whose other cases insist the two agree. The wire's
free-text search takes that same path (`crud/query/compile.go:580`, `:616`) under
a doc comment at `:565` that says "search builds a **case-insensitive** OR" — the
comment is false. The portable spelling is `crud.LikeIgnoreCase`
(`crud/predicate.go:430-434`), reachable from the wire as `ilike`
(`crud/query/filter.go:280`), and it renders `LOWER(col) LIKE LOWER(?)`, which no
ordinary btree index can serve: a full scan on the table the admin screen is
searching.
Point 7 **fails as consistency, correctly as behaviour.** `GetAll` is
deliberately not capped (`repository.go:271-285` says why, pinned by
`TestGetAllIsNotCappedByMaxLimit` at `paging_edge_test.go:91`), because the
decorators that read a whole set to check it would otherwise check the first *n*
and let the rest through. That is right, and it means the same intent returns 50
rows through `Get(crud.Unpaged())` and 5,000,000 through `GetAll()` — one
truncating in silence, the other loading the table into memory, neither raising
anything. Nothing states this in the module reference.
**If not ready:** For point 2, either report the true count for a capped
`Unpaged` request (a second `COUNT`, which the caller asked for by asking for
everything) or refuse `Unpaged` outright when `MaxLimit` is declared. For point 6,
say in the module reference which spelling is portable and what it costs; the
false comment at `crud/query/compile.go:565` is `crud/query`'s to fix. For point 7,
one paragraph: `GetAll` is every matching row and `MaxLimit` does not apply to it.

### H-SQLREPO-05 — Create must not overwrite, and sync must not duplicate
**Who:** an engineer on a `POST /users` endpoint, and the author of a nightly sync from a CRM
**Wants:** "insert, and tell me if it already exists" and "insert or update, keyed on the email"
**Story:** The create endpoint takes a client-supplied id for idempotency, and
must answer 409 rather than silently replacing the row. The sync job holds an
external record with an email and no local key, and wants one statement that
lands it whether or not the row is there.
**Must hold:**
1. A create that names an existing key is a refusal a transport can turn into 409, not a silent overwrite.
2. A write can be keyed on a unique column that is not the primary key.
3. Neither of those is a read-then-write, because two workers running the sync at once would both read absent and both insert.
**Today:** ❌ missing
**Evidence:** `crud/sqlrepo/repository.go:609-624` — a set key always takes the
upsert branch and the conflict target is fixed at `:59`,
`r.upsertTail = d.Upsert(m.PK.Column, upd)`. `crud/dialect.go:80` and `:126`
render `ON CONFLICT (pk)` / `ON DUPLICATE KEY UPDATE`. There is no
`crud.OnConflict` option and no `sqlrepo` setting for a conflict target.
Point 1 is reachable and silent: `port.Sanitize` (`port/model.go:14-21`) clears
only an `auto` key, so a `uuid` or `slug` key the client sent goes straight to
`repo.Save` (`port/service.go:152-167`) and upserts. The sentinel exists —
`crud.ErrConflict` at `crud/errors.go:31` — and nothing raises it here, so the
create path has nothing to call.
**If not ready:** Today the sync job writes `Exists`-then-`Save`, which races on
two axes (see H-SQLREPO-24: the `Exists` half is also replica-eligible), or drops
to a hand-written statement. Closing it is bigger than one line:
- it amends [[D-011]], whose decision table renders the key-set branch as
  `INSERT … ON CONFLICT (pk) DO UPDATE`. The target is part of that decision.
- it does not help the story as written. The sync holds a key-*less* model, and
  `Save` routes that to `insertGen` (`repository.go:619`), a statement built
  without `upsertTail` at all. Making the key-less branch carry a conflict tail
  changes D-011's other row too, and `refresh` (`:673-680`) re-reads by primary
  key, so an email-keyed upsert on a dialect without `RETURNING` reads nothing
  back and answers `ErrNotFound` for a successful write.
- `Upsert(pk string, cols []string)` is a method on the exported `crud.Dialect`,
  so widening it to a target list is a breaking interface change — cheap before
  the tag, expensive after — and `crud/probe/plan.go:238-244` (`full.swallowed`)
  decides which unique violation a fault may claim from
  `UpsertSwallowsPrimaryKeyOnly()`. With a declared non-PK target that answer is
  backwards and the probe attributes the wrong constraint.
- a named column with no unique index behind it is PostgreSQL `42P10`, a 500,
  unless the setting is checked against the catalog at `Bind`.
The 409-on-create half needs one behaviour from here — a refusing conflict mode
returning `crud.ErrConflict` — and the `port` sweep owns the rest.

### H-SQLREPO-06 — Twenty thousand rows from a supplier file
**Who:** the author of a nightly import job
**Wants:** to write the batch without twenty thousand round trips
**Story:** They parse the file into a slice of models and call the batched save.
They expect it to be fast, to be one transaction with the rest of the job, and to
hand back the keys so the next table can reference them.
**Must hold:**
1. A batch of any size the job actually produces goes through.
2. The keys the database generated come back, or the caller is told which dialects cannot say so.
3. Server-owned columns are filled in the models afterwards, the way they are after a single save.
4. The batch joins a transaction the job already opened.
5. A supplier file where some rows carry an id and some do not is the normal shape, and the library either handles it or says why not.
**Today:** 🟡 partial
**Evidence:** 2, 4 and 5 hold, and 2 and 5 are *documented refusals* rather than
holes — worth stating plainly, because both look like gaps from the outside. 4 is
unconditional: `repository.go:1183` and `:1211` both go through `r.exec(ctx)`,
which returns the context executor when one is bound (`:95-101`). 2 holds on
`RETURNING` dialects (`TestSaveAllReadsGeneratedKeysBackWhereItCan`,
`test/integration/saveall_test.go:91`) and is refused in writing on the others:
the doc comment at `repository.go:1120-1123` says MySQL reports one
`LastInsertId` and only guarantees contiguity under some settings, "so reading
them back from it would be a guess", and `saveall_test.go:106-116` pins the
silence with a negative assertion. 5 is refused at `repository.go:1145-1148`
(`TestSaveAllRefusesAMixedBatch`, `saveall_test.go:74`) so the call stays one
round trip or none — but it is a ceiling nobody has written down, and the
author's loop has to partition by key presence before it can chunk, which is
exactly the shape H-SQLREPO-05's sync produces.
Point 1 **fails**: `repository.go:1156-1176` builds **one** statement with one
placeholder per value and never chunks. Twenty thousand rows over six insertable
columns is 120,000 placeholders; PostgreSQL's extended protocol refuses past
65,535, so the job dies inside the driver with a message that names nothing about
vv. The library already knows this limit and handles it in the other place it
bites — `crud/preload.go:17` sets `preloadBatch = 900` and `:221` chunks against
it — and the wire compiler caps `IN` lists for the same reason
(`crud/query/compile.go:39-46`). The write path has neither.
Point 3 **fails and is not documented anywhere.** `:1211-1216` returns with no
read-back at all on a dialect without `RETURNING`, so a batch of *assigned-key*
rows — where the keys were never the question — comes home with no `created_at`
and no other `generated` column, while a single `Save` of the same row has both
(`repository.go:667` re-reads unconditionally, and the comment there says why).
The doc comment covers the keys and says nothing about the rest.
**Nothing in `crud/sqlrepo` tests `SaveAll` at all**, and **no use case covers
it**: [[UC-008]]'s guarantees are all `UpdateAll`/`DeleteAll`, and its "Out of
scope" section opens with "**Bulk insert.**"
(`UC-008-write-many-rows-in-one-statement.md:52-57`). The module reference cites
`[[UC-008]]` for `SaveAll` anyway.
**If not ready:** Today the job chunks in its own loop, at a size it guesses,
partitions by key presence, and on MySQL assigns keys itself. Closing point 1 is
automatic chunking derived at `Bind` — the dialect knows its placeholder ceiling
and the blueprint knows `len(fields)` — but it is not free of semantics: see the
DX section, because chunking turns all-or-nothing into partially-written and the
doc comment at `:1116-1119` currently argues against it. Point 3 is a read-back
on the non-`RETURNING` path when the model declares any `generated` column.
Either way, `SaveAll` needs a use case of its own and a `crudtest` file that pins
the chunk boundary.

### H-SQLREPO-07 — Deactivate every trial account past due
**Who:** an engineer on a billing job
**Wants:** one statement across a filter, and a number back
**Story:** They call the filtered update with a two-field DTO and a predicate.
They want the repository's own narrowing to apply, so the job cannot reach rows
the repository exists to hide, and they want to know how many rows moved.
**Must hold:**
1. One statement, whatever the size of the set.
2. A field the DTO does not define is never written; a field defined as null writes `NULL`.
3. A DTO defining nothing writes nothing.
4. The repository's permanent narrowing is in the statement.
5. The optimistic-lock counter advances on every row written, so a concurrent single-row update built from an earlier read is refused rather than sailing past.
6. The caller is told what the number means, because the two engines do not mean the same thing by it.
7. An option the statement cannot express is refused rather than ignored.
8. A filtered write with no filter at all is something the caller has to mean.
**Today:** 🟡 partial
**Evidence:** 1–5 hold. `repository.go:834` builds one `UPDATE`, `:840-844` is the
empty-DTO no-op, `:857-859` advances the version, `:862` ANDs the repository
scope. Pinned by `updateall_test.go:15-90` and
`TestUpdateAllAdvancesTheVersionOfEveryRowItWrites` (`version_test.go:136`).
**The module reference contradicts point 5 in writing**: `docs/modules/en/sqlrepo.md:92-93`
says `UpdateAll` "neither diffs nor advances a version column", while the code
advances it and [[UC-008]] guarantee 8 requires it. A consumer who reads that
line and concludes their optimistic lock is unprotected against a bulk write has
been told the opposite of the truth.
Point 6 is documentation, and it exists in the right place and not in this one:
`crud/repo.go:92-94` says PostgreSQL reports matched rows and MySQL changed rows.
Nothing in `crud/sqlrepo` normalises it and nothing should; the count returned at
`:870` is `res.RowsAffected` unmodified. What a caller may conclude is written in
[[UC-008]] guarantee 5 — never "the row was not there" from a zero.
Point 7 **fails, and it is this module's own**: `UpdateAll` (`:834-871`) and
`DeleteAll` (`:903-918`) emit no `LIMIT`, and neither refuses one.
`crud.Limit(10)` on a filtered write is accepted and writes every matching row.
On its own that is a caller surprise; under the security gate it is worse, and
the gate-side severity is `docs/ai/usecases/modules/security/Security.md`
H-SECURITY-07 — not restated here. That is [[D-026]], `Status: open`, covering
`DeleteAll` **and** `UpdateAll`, and its option 3 — refuse paging options on the
filtered writes — is the one that lives in this module.
Point 8 **fails here and is closed one layer out.** `DeleteAll(ctx)` with no
options against a bare `Define(...).Bind(src)` renders `DELETE FROM users`,
narrowed by the blueprint scope and nothing else; `UpdateAll` is the same shape.
The guards a reader would assume exist are elsewhere: `AllowUnscopedDeleteAll`
and `AllowUnscopedUpdateAll` in the gate
(`crud/decorators/security/security.go:660`, `:724`) and the empty-specification
refusal in the specs decorator ([[UC-008]] guarantee 9). Undecorated, the verb
does what it says.
**If not ready:** Refuse `Limit`, `Page` and `Offset` on `UpdateAll` and
`DeleteAll` in the repository. It is a few lines, it settles D-026 without the
gate changing, and it turns a write that quietly does more than it was asked into
an error at the call site. Point 8 is a paragraph in the module reference naming
where the guard is, not a change here — a repository verb that deletes every row
is the verb doing its job.

### H-SQLREPO-08 — Tombstones, and the admin who has to see them
**Who:** an engineer on a product where deletion is reversible
**Wants:** one line that makes deleted rows stop existing for the public API, and a way for the admin tool to see and restore them
**Story:** They add the setting to the declaration. Deletes stamp instead of
removing, and every read hides the stamped rows. Then support asks to see what a
customer deleted last week, and to put one back. Later a customer whose account
was deleted signs up again with the same email.
**Must hold:**
1. One setting turns both halves on together, so the reads and the deletes cannot fall out of step.
2. No query option, filter or specification puts the hidden rows back.
3. **No write puts them back either.**
4. Deleting a tombstoned row again changes nothing and does not double-count.
5. There is a supported way to see the tombstones.
6. There is a supported way to restore one.
7. The tombstone column is the repository's, not the client's — the way the version column is.
8. What a tombstone does to a unique index is stated, because the row still holds the email.
**Today:** 🟡 partial
**Evidence:** 1, 2 and 4 hold and are proven. `blueprint.go:202-222` validates the
column and folds `IS NULL` into the permanent scope in the same step, which is
what makes 1 structural rather than remembered; `repository.go:933` is the stamp,
under exactly the narrowing the `DELETE` would have had — the comment at
`:920-932` names the bug that taught it. [[D-031]] and [[UC-016]] carry the
reasoning; `test/integration/softdelete_test.go` runs it live, including the
control `TestWithoutTheSettingADeleteStillRemovesTheRow` (`:217`).
Point 3 **fails, and it is the sharpest hole in the module.** Soft delete is
implemented as a fold into `Scope`, and `Scope` cannot reach `Save` — see
H-SQLREPO-18. The tombstone column is an ordinary updatable column
(`crud/meta.go:289-299` puts every non-PK, non-`generated`, non-`immutable`,
non-`version` column into `s.Update`), so `r.upsertTail` (`repository.go:59`)
renders `deleted_at = EXCLUDED.deleted_at` and a `Save` carrying a tombstone's
key sets it from the model, which holds the zero value. The row is un-deleted and
the read-back at `:667` — which passes `within = nil` — hands it back as a
success. Reachable through the ordinary create endpoint for any client-owned key
(`port/service.go:152-167`) and through `PUT` (`:190-211`).
**The gate does not close this one, and its own probe is what makes it worse.**
`gate.saveTarget` decides "does this row already exist" with
`g.Core.Exists(ctx, byID, crud.PrimaryOnly())`
(`crud/decorators/security/security.go:541`), which runs under the blueprint
scope that hides the tombstone — so `existing == nil`, `action = Create`, and the
write is authorised against the *create* permission. That is written down in
[[UC-016]]'s Status ("The warning is the create path",
`UC-016-hide-rows-permanently-at-the-repository-level.md:88-94`) and as open
tension 17 in `docs/ai/usecases/Index.md:257-261`. There is no control test in
`test/integration/softdelete_test.go`.
Point 5 works and is undocumented in the module reference: a second `Define` over
the same table without the setting gives a tombstone-visible repository, which is
this repository's own idiom — `SoftRows` and `RawRows` at
`test/integration/softdelete_test.go:38-39`. What it costs is not written down
anywhere: the second blueprint has to repeat every safety-relevant setting
(`Scope`, `RelationScope`, `MaxLimit`, `DefaultSort`) *and* every middleware at
its own `Bind`, and its `Delete` is a physical `DELETE`. The tombstone view is
the one repository where a forgotten `security.Gate` is most dangerous, because
it is the repository that can see the rows everything else hides.
Point 6 has no verb — there is no `Restore`, and `Update` on the soft-deleting
repository cannot reach a tombstoned row because the load is scoped.
Point 7 **fails**: `crud/update.go:112-120` freezes the key, `generated`,
`immutable` and `version` columns against an update DTO, and the tombstone column
is none of those. [[D-031]]'s own "Why" names the outcome it chose against —
"either putting the tombstone into the caller's update DTO — where a client could
`PATCH` it — or adding a raw-column verb to the seam" — and the implementation
did not close it. The default pushes the wrong way: the generated file asserts at
package init that the DTO covers every writable column
(`port.MustCoverUpdate`, `port/pathmap.go:228-232`; the shape is
`test/gormstore/vv_gen.go:118-120`), so hand-removing the field panics at
start-up unless the exclusion is declared at generation time.
Point 8 is stated where it belongs and nowhere a consumer reads: the `SoftDelete`
doc comment (`blueprint.go:108-110`) says a unique index still sees the
tombstones and the answer is a partial index the library cannot write, and
[[D-031]] forbids promising otherwise. The module reference's soft-delete section
does not carry it.
**If not ready:** For point 3, either narrow `Save` (see H-SQLREPO-18) or, at
minimum, exclude the tombstone column from `upsertTail` the way `immutable` and
`version` are already excluded — a save then cannot un-delete even though it can
still overwrite. For point 7, freeze the column in `PlanFor`, which needs the
plan to know which column it is; today `PlanFor` is handed the schema, which does
not carry the setting. The generator flag that expresses this today is
`-readonly DeletedAt`, **not** `-skip` — `-skip` leaves the field out of the
metamodel too, so `sqlrepo.SoftDelete(Doc_.DeletedAt.Name())` stops compiling and
the column becomes unfilterable (`cmd/vv/main.go:22-24`;
`test/gormstore/model.go:6` is the fixture that gets it right).
Points 6 and 7 are one item, not two: freezing the column removes the only
restore path there is. A `Restore` verb is not cheap — [[D-030]] makes any
addition to `crud.Core` an obligation on `security.gate`, and `coreVerbs` in
`crud/decorators/security/obligation_test.go` fails the build until the override
exists — and it has to check a permission of its own, because un-deleting through
the update permission is point 7 wearing a different name.

### H-SQLREPO-09 — Two people editing the same record
**Who:** an engineer whose support tool and customer portal both write the same row
**Wants:** the second save refused rather than silently erasing the first
**Story:** They tag an integer column and change nothing at the call sites. Two
patches race. The loser is told to retry.
**Must hold:**
1. The column is the whole opt-in.
2. The write pins itself to the version it read and advances the counter in the same statement.
3. The loser gets a conflict a transport turns into 409, and the winner's row is intact.
4. A row that is genuinely gone is a different error, because "give up" and "retry" are different instructions.
5. A no-op patch does not burn a version.
6. That distinction is made from the primary, not from a replica.
**Today:** 🟡 partial
**Evidence:** 1–5 hold and are proven hard — `repository.go:744-751` writes both
halves, `:801-812` builds the pin, and `version_test.go:34-190` plus [[UC-009]]
cover the race by firing a competing write into the exact gap. Point 6 fails.
`repository.go:821` classifies the missed row with `r.Exists(...)`, and `Exists`
issues its query through `r.read(ctx, o)` at `:595` with no `crud.PrimaryOnly()`
— so with a `crud.ReadWrite` source that read goes to the replica. Its answer
decides between `ErrNotFound` (stop) and `ErrStaleVersion` (retry), which is
precisely the read [[D-032]] forbids sending to the replica: *"Do not route a read
that decides a write to the replica. If a new such read appears, it gets
`PrimaryOnly` in the same change."* Under lag it goes wrong in both directions —
a contended new row reads absent and the caller is told 404 for an edit it should
have retried, and a row deleted on the primary reads present and the caller
retries forever. No test covers it; `test/integration/replica_test.go` pins the
load half of `Update` and the gate's checks, not this one.
**If not ready:** Add `crud.PrimaryOnly()` to the `Exists` call at
`repository.go:821` and a test beside `TestUpdateDiffsAgainstThePrimary`. One line
and one test. It also has doc consequences the fix must carry: `missedRow` is not
listed in [[D-032]]'s "Where it lives"
(`docs/ai/decisions/D-032-a-replica-never-decides-a-write.md:59-64`, which names
`read` and `Update` and no third site), nothing in its "Proven by" covers it,
`UC-009`'s Status reads flatly "covered"
(`docs/ai/usecases/modules/sqlrepo/UC-009-survive-concurrent-writers.md:80-86`),
and the Index's open tensions do not list it. One of those is wrong today.

### H-SQLREPO-10 — Two writes that must happen together
**Who:** an engineer on a checkout handler
**Wants:** a transaction that reaches both repositories, whether it opens here or upstream
**Story:** The handler writes an order and decrements stock. Sometimes the
service method already runs inside a transaction the ORM opened, and the two
writes have to join it rather than start a second one. Sometimes the two
repositories are on different databases and only one of them belongs in this
transaction.
**Must hold:**
1. Opening a transaction is one call, and the block commits on nil and rolls back on error.
2. A transaction already on the context is joined, not nested — the outer owner keeps commit and rollback.
3. A transaction some other library owns can be handed over in one line.
4. Every statement the repository issues on that context goes to that executor, the re-read after a write included.
5. Inside a transaction, a partial update locks the row it loaded.
6. The isolation level can be chosen where the work needs it.
7. A repository on another database does not join this transaction by accident.
**Today:** 🟡 partial
**Evidence:** 1, 3, 4 and 7 hold and are executed. `crud/executor.go:503` joins
when the context already carries an executor for this source,
`crud/sqlrepo/repository.go:137` delegates to it, `:712-715` adds `FOR UPDATE`
only when a transaction is present. Handing one over is `crud.WithExecutor`, one
line (`crud/executor.go:316`), proven across `database/sql`, pgx, sqlx, gorm, ent
and sqlc by [[UC-005]]. Point 7 is `exec()` at `repository.go:95-101`: three lines
that ask `crud.ExecutorFor(ctx, r.src)` and leave somebody else's database alone —
the rest of that mechanism is `crud/executor.go`'s and is H-SQLREPO-12.
Points 2 and 5 are ❓ **unverified, not proven** — structural in the code and
recorded as gaps by [[UC-005]] itself
(`UC-005-run-repository-work-in-an-orm-transaction.md:101-108`) and as open
tensions 7 and 8 in `docs/ai/usecases/Index.md:208-215`. No test asserts an inner
failure leaving the outer transaction to decide, and no test shows a partial
update taking the row lock *by itself* when a transaction is present; only an
explicitly requested lock is covered.
Point 6 has no knob: `crud.Beginner` is `Begin(ctx)` with no options
(`crud/executor.go:49-54`), and the level is a property of the *datasource* —
`crudsql.DB.WithTxOptions` at `crud/adapter/crudsql/crudsql.go:173`. So "this one
transaction is serializable" means binding a second repository over a second
datasource, or opening the transaction yourself and handing it in.
**If not ready:** The escape hatch is real and short — open the transaction with
the driver, wrap it, put it on the context. But a consumer who wants one
serializable block has to leave the short path entirely, and there is no retry
helper for the serialisation failure that shape exists to produce (see
H-SQLREPO-25: there is no sentinel to retry *on*, either). The fix does not live
in `sqlrepo`: `repository.Tx` is a one-line delegation to `crud.InTx`, and the
missing seam is on `crud.Beginner` and the adapters. See the DX section for the
shape. [[UC-005]] already records that a savepoint is unreachable under a gorm-
or sqlx-owned transaction for the same reason: what is handed over there is a
bare executor.

### H-SQLREPO-11 — Reads to the replica, writes to the primary
**Who:** an engineer whose read traffic outgrew one database
**Wants:** the split without auditing every call site
**Story:** They wrap the two handles in one source and rebind. Reads move.
Writes and anything that decides a write stay put.
**Must hold:**
1. Wrapping is one line and no call site changes.
2. A read inside a transaction goes to that transaction.
3. The load half of a partial update comes from the primary, or the diff is against a row as it was.
4. Every check an authorisation policy makes comes from the primary. *(`crud/decorators/security`'s guarantee; this module only honours `crud.PrimaryOnly()` when the gate passes it.)*
5. Any other read whose answer decides a write also comes from the primary. → **See H-SQLREPO-09 point 6.** Same line, same defect; counted once.
6. A caller can force a single read onto the primary.
**Today:** 🟡 partial
**Evidence:** 1, 2, 3 and 6 hold, and are the strongest-tested part of this area —
`repository.go:109-118` is the three-rule router, `:712-715` marks the update's
load `PrimaryOnly`, and `test/integration/replica_test.go:24-186` runs it against
two databases holding *different* rows so the answer names which one replied.
Also worth the owner's eye — `crud/sqlrepo/blueprint.go:33` declares a
`replica crud.Source` settings field that nothing reads or writes; the replica is
discovered at `Bind` through `crud.ReadSourceOf` (`repository.go:33-38`), which
is right, and the dead field suggests a setting that was designed and then
correctly abandoned.
**If not ready:** See H-SQLREPO-09. The dead field costs nothing at run time but
will read to the next agent as a setting that exists.

### H-SQLREPO-12 — Application data here, events over there
**Who:** an engineer in a process that writes its own tables and an analytics database
**Wants:** a transaction that reaches exactly the repositories on the database it belongs to
**Story:** The handler opens a transaction on the primary, writes a user, and
emits an event to the analytics database. A rollback must take the user and leave
the event, and neither write may land on the wrong server.
**Must hold:**
1. A repository leaves alone a context executor bound to somebody else's database.
2. Everything else about the scoped binding — naming a handle, naming a source over it, a `Tx` scoping itself — is `crud/executor.go`'s. *(Pointer only; the `crud` sweep owns it.)*
**Today:** ✅ ready at this layer
**Evidence:** Point 1 is `crud/sqlrepo/repository.go:95-101` and is the entire
`sqlrepo`-visible surface of this. [[UC-012]] covers the rest with tests against
two real PostgreSQL databases, and [[D-027]] records that the unscoped capture is
deliberate.
**If not ready:** n/a here. The failure a consumer meets on this path is
[[UC-012]]'s blind spot 2 and open tension 18 — naming a *transaction* rather
than the database keys the binding on a handle no repository matches, so the
write goes to the pool outside the transaction and reports success. That is
`crud/executor.go:335`'s to fix, is a release blocker in the `crud` sweep, and is
carried here as a cross-reference only.

### H-SQLREPO-13 — `DELETE /users/42`
**Who:** an engineer wiring the delete endpoint, and the author of a bulk cleanup screen
**Wants:** the row gone, an honest answer when it was not there, and a usable error when the database refuses
**Story:** The handler deletes by id. Sometimes the id names a row this
repository is not allowed to see. Sometimes the screen sends five ids and three
of them exist. Sometimes the row has children and a foreign key says no.
**Must hold:**
1. Deleting a row the repository's narrowing hides is not a success.
2. A batch of ids reports how many it removed, and the caller can tell that from an error.
3. A row a foreign key still references comes back as something a handler can turn into 409, not as a driver string.
4. A soft-deleting repository and a hard-deleting one answer the same way to the same call.
5. A batch of ids of any size the screen can produce goes through.
**Today:** 🟡 partial
**Evidence:** 1 holds *in the statement* and is handed to the caller as a number,
not an error: `repository.go:878-887` ANDs the repository scope into the `WHERE`,
so a row outside it matches nothing and `Delete` returns `0, nil`. The comment at
`:874-877` names the bug that taught it — "GET /:id answers 404 and DELETE /:id
answers 200 for the same row" — and the control pair is
`TestScopeReachesDeleteByID` and `TestDeleteWithoutAScopeIsStillJustTheKey`
(`paging_edge_test.go:18`, `:47`). Turning the zero into a 404 is the caller's
job, and `port` does it for the single-id verb only
(`port/service.go:217-226`); `DeleteMany` (`:230-235`) passes the count through,
so "three of five" is a 200 with `3` and no way to learn which two were missing.
2 and 4 hold. 3 holds better than a reader would expect: the adapters classify
integrity errors, so a bare `Define(...).Bind(src)` — no decorator — returns
something wrapping `crud.ErrConflict` for a duplicate key, a dangling foreign key
and a `NOT NULL` violation on every adapter and engine
(`crud/adapter/crudsql/conflict.go:16`, `crud/sqlfault/classify.go:137-157`,
pinned by `TestIntegrityViolationsAreClassifiedByEveryAdapter` at
`test/integration/dialect_edge_test.go:747`). What a bare binding does *not* give
is which constraint, and on some sources not even a code — see H-SQLREPO-14.
5 **fails for the same reason as H-SQLREPO-06 point 1, and it is the same fix,
not a second one**: `crud.InAny` renders one placeholder per id
(`repository.go:882`) and nothing chunks, so a select-all-and-delete over a big
page dies inside the driver.
**If not ready:** Today the handler maps `0` to 404 itself, and a bulk screen
chunks its own id list. The count semantics are worth stating in the module
reference: `Delete` returning 0 is "nothing in reach", and a caller cannot tell a
missing row from a hidden one — which is the point.

### H-SQLREPO-14 — The email is already taken
**Who:** an engineer on `POST /users`, on the first day of real traffic
**Wants:** a duplicate to reach the client as 409 naming the field, not as a 500
**Story:** Two people register with the same address. The second insert is
refused by a unique index. The handler has to answer something a form can render
next to the email box.
**Must hold:**
1. The boundary is stated: what a bare `Bind` gives, what needs an adapter, and what needs a decorator. Everything below is one layer or another and none of it is `sqlrepo`'s.
**Today:** 🟡 partial
**Evidence:** `grep -rn 'sqlerr\|errs\.' crud/sqlrepo/*.go` is empty, which is
correct — the classification belongs to the adapter and the decorator — and
nothing in the repository's own documentation tells a consumer where the boundary
runs. The three layers:
- **The sentinel comes free, on every source built by an adapter.** The executor
  wraps every statement (`crud/adapter/crudsql/crudsql.go:91`, `:108`) and
  `sqlfault.Wrap` classifies on SQLSTATE class 23 with no classifier configured
  at all (`crud/sqlfault/classify.go:137-157`), so the caller gets
  `crud.ErrConflict` and the transports answer 409.
  `TestIntegrityViolationsAreClassifiedByEveryAdapter`
  (`test/integration/dialect_edge_test.go:747`) proves it on a plain
  `EgConses.Bind(tg.src)` with no decorator, on five distinct sources, with a
  control that the error must not `errors.Is` any of `ErrNotFound`,
  `ErrMissingID`, `ErrReadOnly` or `ErrForbidden`.
- **The `errs.Code` does not come free.** A source built with `crudsql.From`
  (`crudsql.go:75-77`) names no engine and gets no classifier ([[D-046]]) — which
  is how the ent stack is reached — so no fault and no code arrives, only the
  sentinel. That is asserted, not assumed: `dialect_edge_test.go:806-812` compares
  `errs.AsFault(err)` against a per-target `classifies` flag, and
  `test/integration/edge_test.go:333-352` sets it `false` for both ent targets.
  A consumer who writes their own `crud.Source` lands in the same place and no
  test says so.
- **The field name needs a decorator and a catalog**: `faults.Enrich`
  (`crud/decorators/faults/faults.go:52`) at `Bind`, plus `crud/catalog` for the
  constraint-to-field hop.
**If not ready:** Nothing to write by hand for the sentinel. For the code on an
ORM-backed source, name the engine at the adapter. For the field, add
`faults.Enrich[User, int64]()` to the `Bind` call. The module reference should
carry one paragraph naming the three layers, because "what does a failed insert
return" is the first question every create endpoint has and this page does not
answer it.

### H-SQLREPO-15 — The dashboard
**Who:** an engineer building the account overview screen
**Wants:** counts by status and sums per month, under the same narrowing as the list screen
**Story:** They write a handler of their own — an aggregate is deliberately not
reachable from the wire — that returns a row per status with a count, and another
that returns a row per month with a sum. Both must see exactly the rows the list
screen sees: the tenant scope, the tombstone filter, and whatever the policy
narrows.
**Must hold:**
1. Grouping and aggregating is a call on the repository, not a reason to leave for raw SQL.
2. The repository's permanent narrowing and the per-request one both apply.
3. A field the model does not have is refused, the way it is in a filter.
4. Every group the data has comes back, because a dashboard that silently drops rows is worse than one that fails.
5. `Unpaged` means the same thing here as it does on a list.
**Today:** 🟡 partial
**Evidence:** 1–3 hold and are executed against both engines.
`repository.go:1028-1044` renders the aggregate, ANDs `r.scoped(o)` and carries
`r.relScopes(o)`; `o.Agg.Validate(r.meta)` refuses an unknown field at `:1030`.
`test/integration/aggregate_test.go:38-197` covers grouping, the filter, the
permanent scope, the gate and an unknown field. The "not a reason to leave" in
point 1 is [[D-029]]'s own argument, and D-029 also settles the shape of the
story: `Aggregate` is not reachable from `query.Request` — `grep -rn Aggregate
crud/query/ crud/http/ port/` is empty — because "an application exposes the
specific totals it wants; a client cannot ask for an arbitrary `GROUP BY`". The
endpoint is hand-written, on purpose.
Point 4 **fails, and silently**. An aggregate is paged like a list:
`repository.go:1051` resolves the caller's limit against the blueprint's
`defaultLimit`, which is 20 (`blueprint.go:26`), and `:1055` applies it. "Count by
status" over 25 statuses returns 20 groups, in whatever order the engine chose if
no sort was asked for, with no total and no `HasNext` to make the truncation
visible — `Aggregate` returns a bare `[]crud.AggregateRow`. Nothing tests it: the
integration cases group into two buckets, so the limit never bites.
Point 5 **fails, in the direction nobody would guess.** `crud.Unpaged()` on `Get`
and `GetAll` is clamped to `MaxLimit` — `crud/options.go:238-247` says so in
words ("Unpaged is honoured only as far as maxLimit") and the module reference
promises it ("Clamps even an `Unpaged()` request",
`docs/modules/en/sqlrepo.md:104`). On `Aggregate` it is not: `:1051` computes the
clamped limit and `:1052-1054` throws it away with `limit, offset = 0, 0`. So the
one workaround for point 4 is also the one call in the module where `Unpaged`
escapes the declared cap. One option word, two meanings, one repository.
There is also a decorator interaction the consumer meets here and cannot see from
this module: with any `Inspect` rule set, switching `InspectReads` on makes every
`Aggregate` return `Denied` — `docs/ai/usecases/modules/security/Security.md`
H-SECURITY-07 point 5, cross-referenced, not restated.
**If not ready:** Today the caller passes `crud.Unpaged()` and nobody tells them
to, and on this verb that also drops their `MaxLimit`. Either default an
aggregate to unpaged — the argument being that a group-by result is not a page
and the row count is bounded by the cardinality of the grouping columns, not by
the table, and that D-029 already keeps the grouping out of a client's hands — or
keep the cap and give the result a way to say it was cut. The first is a
behaviour change and belongs in a decision doc; the second is a struct field.
Either way the `Unpaged` inconsistency at `:1052-1054` should go with it.

### H-SQLREPO-16 — The detail page and its comments
**Who:** an engineer building `GET /articles/42?preload=comments,author`
**Wants:** the row and its relations in a bounded number of statements
**Story:** The article page needs the author and the comments. The list page
needs them for twenty articles. Comments are soft-deleted, and the deleted ones
must not appear on either.
**Must hold:**
1. Loading relations is a query option, and the number of statements does not grow with the number of rows.
2. A client cannot turn one request into an unbounded number of statements by asking for a deep path.
3. The repository's own narrowing follows a relation that lands back on its own model, at any depth, without being declared.
4. The narrowing that belongs on *another* model is declarable, and a typo in the path is refused at declaration time.
5. If a consumer declares nothing, the far side is unnarrowed — and they are told, rather than finding out from a support ticket.
6. A child model that declares its own soft delete stays soft-deleted when it is reached as somebody else's relation.
7. A client cannot turn one request into an unbounded number of *rows*.
**Today:** 🟡 partial
**Evidence:** 1–5 hold. Preloading is one statement per hop with the parent keys
chunked at 900 (`crud/preload.go:17`, `:221`); depth is capped by `PreloadDepth`
with a default of 5 (`crud/preload.go:14`,
`TestPreloadDepthCapsAPathAndZeroMeansUnset` at `blueprint_edge_test.go:258`).
Point 3 is `resolveRelationScopes` registering the blueprint's scope under its own
model type (`blueprint.go:228-237`), pinned by
`TestAPreloadOfTheRepositorysOwnModelCarriesItsScope` and
`TestANestedPreloadOfTheSameModelCarriesTheScopeAtEveryLevel`
(`relscope_test.go:36`, `:56`). Point 4 is `RelationScope`, validated against the
model at `Define` (`TestRelationScopeRefusesAPathTheModelDoesNotHave`,
`relscope_test.go:216`) and pinned end to end by
`TestRelationScopeNarrowsBothThePreloadAndTheFilterHop` (`:145`) with the control
`TestACallerCannotWidenARelationScope` (`:192`). Point 5 is [[D-007]].
Point 6 **fails, and it is the intersection of the two features this module
owns.** The preloader is handed only *this* repository's relation scopes
(`repository.go:531-536` passes `r.relScopes(o)` into `crud.RunPreloads`), and no
registry exists for it to consult — a blueprint's soft-delete fold lives on
`bp.set.scope` (`blueprint.go:220`) and is reachable from nothing but that
blueprint. So `sqlrepo.SoftDelete("DeletedAt")` on the `Comment` blueprint is
invisible to `Articles.Get(ctx, crud.Preload("Comments"))`: the tombstones come
back. The story in this case says the deleted comments "must not appear on
either", and the way to get that is `sqlrepo.RelationScope("Comments",
crud.IsNull("DeletedAt"))` on the *article* blueprint — the same fact declared
twice, in two files, with nothing checking they agree.
Point 7 **fails, and it is a hard refusal rather than an oversight.** Point 2
bounds *statements*, which a reader takes as "a client cannot blow this request
up". `GET /articles?limit=50&preload=comments` loads every comment of all 50
articles into memory in one statement. There is no per-relation limit and asking
for one is refused by design: `PreloadWhere` rejects `Limit`, `Page`, `Offset`
and `Unpaged` (`crud/preload.go:39-41`, `:193-196` — "a preload cannot be
paginated; it is loaded for every parent at once"), because a `LIMIT` on a
batched preload truncates some parents' children and not others. That reasoning
is right. The consumer's fix is a second query against the child repository, and
nothing tells them.
The former declaration-order trap is closed. `RelationScope` now validates its
canonical path through `Meta.ValidateRelationPath`, which previews target table
names without calling the caching/publishing `Relation.Target`. `TryDefine`
publishes the root only after every scope is valid and publishes no target, so a
target blueprint declared later in package initialisation can still register
`blog_comments`; the first actual traversal then caches that published answer.
After an actual traversal, a conflicting late declaration fails explicitly
rather than leaving old and new relations on different tables ([[D-080]]).
**If not ready:** For point 6, either say plainly in the module reference that a
relation's narrowing is the *owner's* declaration and a target's own `SoftDelete`
does not travel, or teach the preloader to consult the table registry the way
`Relation.Target` already does. For point 7, one paragraph: a to-many preload is
unbounded, and the answer to "the article with its most recent ten comments" is a
second query.

### H-SQLREPO-17 — The binary shipped before the migration
**Who:** whoever is on call the evening the deploy order got swapped
**Wants:** to find out from the process, not from every endpoint on that table
**Story:** Somebody adds a field to the model and a column to the migration. The
migration has not run yet, or it ran and the column was named differently. Every
statement for that table now names a column the database does not have.
**Must hold:**
1. A model that disagrees with its table is caught before the process serves traffic, or the author is told plainly that it is not and what to do instead.
2. The failure, when it comes, names vv's field and not just the driver's column.
3. The blast radius is visible: one added field breaks every endpoint on that table, not only the new one.
**Today:** ❌ missing
**Evidence:** Nothing found for this. `grep -rn catalog crud/sqlrepo/` is empty:
the introspection package exists at `crud/catalog/` — four dialects, per-handle —
and this module never reaches for it. `TryDefine` checks the DTO against the Go
struct and never against the database (`blueprint.go:158-196`), and `Bind`
(`:246-249`) issues no statement at all. Every mapped field is in `selectFrom`
(`repository.go:45`), so one drifted field is a driver error on every read of that
table.
**If not ready:** Today a consumer writes their own start-up check or discovers
it in production. Closing it is optional by nature — a `Bind` that talks to the
database is a `Bind` that fails when the database is down at boot, which many
deployments do not want — so the shape is a separate opt-in
(`sqlrepo.VerifyAgainst(cat)` or a package-level helper over `crud/catalog`) and a
paragraph in the module reference saying that nothing checks this by default.
Note that H-SQLREPO-01's must-hold 2 reads, in a consumer's vocabulary, as if
start-up already covered this. It does not, and the module reference should not
let anyone believe it.

### H-SQLREPO-18 — The repository that only ever sees one tenant
**Who:** an engineer wiring multi-tenancy, or anyone who read what `Scope` promises
**Wants:** one declaration that makes every operation this repository performs stay inside the tenant
**Story:** They read that `Scope` narrows the repository permanently, declare it,
and stop thinking about tenants. Reads are narrowed. Deletes are narrowed. The
load half of a patch is narrowed. Then a `POST` arrives carrying a key from
another tenant.
**Must hold:**
1. A read cannot see outside the narrowing.
2. A delete cannot reach outside it.
3. A patch cannot reach outside it, on either half.
4. **A create or replace cannot write outside it, and cannot overwrite a row outside it.**
5. Whatever a write is allowed to touch is what it is allowed to read back.
6. Where the narrowing does not reach, the declaration says so — not a doc comment on one setting.
7. The narrowing can depend on who is asking, because that is what "this tenant" means in a multi-tenant service.
**Today:** 🟡 partial
**Evidence:** 1–3 hold and are proven, including the halves people forget:
`blueprint_edge_test.go:317` (`TestScopeIsANDedIntoEveryStatementWithAWhereClause`),
`:365` (`TestACallerFilterCannotWidenTheScope`), `:384`
(`TestUpdateLoadsThroughTheScopeSoAnOutsideRowIsNotFound`), and
`paging_edge_test.go:18` for `Delete`.
Point 4 **fails, deliberately and in writing — but only half of it is pinned.**
`Save` is an upsert and has no `WHERE` for a scope to narrow, so the insert writes
whatever tenant the model carries and a `Save` with somebody else's key overwrites
their row. The setting says so (`blueprint.go:76-78`: "It cannot apply to Save").
`TestScopeCannotReachSave` (`blueprint_edge_test.go:404-419`) pins the *insert*
half only: the model it saves has no primary key, so the statement asserted is a
plain `INSERT … RETURNING` with no `ON CONFLICT` tail. **The severe half — a keyed
`Save` overwriting an out-of-scope row — has no test anywhere**; `grep -n
scopedUsers crud/sqlrepo/*_test.go` finds one `Save` call and it is that one.
`SaveAll` inherits the same hole through `r.upsertTail` (`repository.go:1152`).
Point 5 **fails** as a consequence: `refresh` takes a `within` parameter for
exactly this, and both call sites in `insert` pass `nil`
(`repository.go:649`, `:667`), so the read-back after a `Save` runs with no
narrowing and hands back a row this repository is not allowed to show.
Point 6 **fails**: the setting's own doc comment (`blueprint.go:76-78`) is the
only place this is written. The module reference says the opposite —
`docs/modules/en/sqlrepo.md:107` describes `Scope` as "a predicate ANDed into
every read and **every scoped write**", which is the exact belief this case exists
to break.
Point 7 **fails, and it is the headline nobody has stated.** `Scope` takes a
`crud.Predicate` fixed at declaration (`blueprint.go:85`), and a `Blueprint` is a
package-level `var` in every example. There is no context in it and no place to
put one, so `sqlrepo.Scope(crud.Eq("TenantID", t))` with a per-request `t` cannot
be written at all — unless the consumer calls `Define` and `Bind` per request,
which they should not: `Bind` is cheap (`blueprint.go:246-249` allocates and
issues nothing) but `Define` mutates a process-global table registry
(`crud.RegisterTable[M]`, `blueprint.go:182` → `crud/relation.go:152`, a
`sync.Map`). That registry now rejects conflicting and late canonical choices
([[D-080]]), which closes declaration-order drift but also makes especially
clear why choosing a table through per-request `Define` is invalid.
Per-request narrowing was always the gate's job. `Scope` is per-table and
per-everyone.
**The gate closes less of this than a reader expects.** `security.Gate` guards
`Save` in two independent halves. `saveTarget`
(`crud/decorators/security/security.go:515-548`) refuses an *overwrite* of a row
the policy scope hides — and returns `nil, nil` outright when `Policy.Scope` is
nil (`:536-537`), so a permission-only policy leaves that half open. The
*incoming-row* check, which is what stops a write **into** somebody else's scope,
is `g.inspect(ctx, action, m)` at `:499-501`, and `gate.inspect` returns nil
immediately when `Policy.Inspect == nil` (`:190-195`). So a policy carrying only
`Scope` refuses the cross-tenant overwrite and lets `Save(&User{TenantID: 999})`
through untouched. The honest statement is: *the declaration narrows reads and
deletes; narrowing writes needs a gate policy that declares both a `Scope` and an
`Inspect`* — and nothing says that today.
**If not ready:** Today the guard is a service method or a fully-specified
`security.Gate`, and a consumer who declared `Scope` and no gate has an
unprotected write surface they believe is protected. The cheap half is point 5:
pass `r.bp.set.scope` as `within` to both `refresh` calls, so a write that landed
outside the narrowing at least cannot be read back through it. See the DX section
for what the other half costs and for the cheapest option, which is not a
statement change at all.

### H-SQLREPO-19 — "Update this object"
**Who:** anyone arriving from gorm, JPA or ent, on their second day
**Wants:** to change a couple of fields on a loaded object and put it back
**Story:** They build a `User` from a JSON body carrying three fields, set the
id, and call `Save` — because the model's own documentation says "key → UPSERT"
and that is the habit every ORM taught them. They expect the other columns to be
left where they were.
**Must hold:**
1. It is stated which columns a keyed `Save` writes and which it leaves alone.
2. A column the model happens to hold at its zero value is not silently written over the stored one — or the caller is told, at the call site, that it will be.
3. There is an obvious verb for "change these fields", and it is not this one.
**Today:** ❌ missing
**Evidence:** Point 3 holds and is the whole answer: `Update` is load-diff-write
([[D-010]], H-SQLREPO-03). Points 1 and 2 have nothing. A keyed `Save` takes
`r.insertFull + r.upsertTail` with argument list `r.meta.Insert`
(`repository.go:623`), and `s.Insert` is *every non-`generated` column*
(`crud/meta.go:289`); the conflict tail is built from `m.Update`
(`repository.go:57-59`), which excludes only the key, `generated`, `immutable`
and `version` columns (`crud/meta.go:288-299`). So `u := User{ID: 42, Name:
"Anna"}; users.Save(ctx, &u)` writes `email = ''`, `age = NULL`, `archived =
false` and `deleted_at = NULL` from the Go zero values, reports nil, and returns
a model that agrees with the row it just flattened. Everything the DTO path in
[[UC-003]] exists to prevent, one verb over.
It is reachable straight from `PUT /{id}` — `port/service.go:190-211` clears
`generated` columns, sets the id and calls `repo.Save` — which is what a `PUT`
means, so on that path it is correct. It is not correct as "update this object",
and nothing distinguishes the two for a caller in Go.
**If not ready:** Nothing to write by hand: `Update` is the verb. What is missing
is one paragraph in the module reference under "Save is JPA-shaped" saying which
columns a keyed `Save` writes, that a partially-populated model is a full
replacement, and that `Update` is the verb for a patch. This is the cheapest item
in this sweep and it prevents the most expensive kind of bug.

### H-SQLREPO-20 — The order and its lines
**Who:** an engineer writing the checkout handler
**Wants:** to save an aggregate root and have the children land with it
**Story:** They declared `rel:"has_many"` on `Order.Items`, watched a preload
fill the slice on the read path, built an `Order` with three `Items` on it and
called `Save`. The order row exists. The items do not.
**Must hold:**
1. Either the children are written, or the call refuses.
2. Whichever it is, it is stated where the relation is declared.
**Today:** ❌ missing
**Evidence:** Neither. Relations are never columns — `crud/meta.go:102-104` says
so and `s.Insert`/`s.InsertGen` are built from `db`-tagged fields only
(`:277-299`) — so `r.meta.Values(m, fields)` at `repository.go:631` never looks at
`order.Items` and the statement has no place for them. There is no write-side
cascade in the tree: `grep -rn cascade --include='*.go' crud/` returns nothing
outside catalog introspection and the probe. `SaveAll` is the same shape. The call
returns nil, the parent row is correct, the children are silently absent, and
nothing anywhere — not the module reference (`grep -in cascade
docs/modules/en/sqlrepo.md` is empty), not [[D-017]], which is about Go-side
hooks — says a word about it.
**If not ready:** Today the handler saves the parent, reads the key back, sets it
on each child and calls the child repository — inside a `Tx`, which is one line
and works. That is a reasonable design and it should be the *stated* design: one
sentence beside `RelationScope` in the module reference, "writes are columns only;
associations are yours", turns a support ticket into a decision. A refusal is the
alternative and it is not free: detecting a non-empty relation slice at `Save`
costs a reflect walk per write on every model that has a relation.

### H-SQLREPO-21 — Sorted by a column on the other table
**Who:** an engineer building the orders list screen
**Wants:** "orders where the customer's country is DE, sorted by the customer's name"
**Story:** The screen shows a joined column, so the filter and the sort both have
to hop a relation. They write the path, not a join.
**Must hold:**
1. Naming a path in a filter or a sort is enough; no join is written by hand.
2. It stays one statement rather than becoming N+1.
3. The narrowing that applies to the far side inside a preload applies inside the hop too.
4. A path the model does not have is refused, not dropped.
5. What it costs is knowable before production, because a hop is not free.
**Today:** 🟡 partial
**Evidence:** 1–4 hold and are the reason the metamodel expands relation paths at
all. A filter hop opens a correlated `EXISTS` and a sort hop opens a scalar
subquery, both carrying the relation scopes: pinned by
`TestARelationFilterCarriesTheScopeIntoItsSubquery` (`relscope_test.go:78`) and
`TestANestedSortCarriesTheScopeIntoItsSubquery` (`:95`), with
`TestRelationScopeNarrowsBothThePreloadAndTheFilterHop` (`:145`) covering the
declared far-side narrowing and `TestACallerCannotWidenARelationScope` (`:192`)
as the control. Refusal of an unknown path is `RelationAt` at
`blueprint.go:230-233` for a declaration and `crud.UnknownFieldError` at query
time for a caller. [[UC-006]] covers it end to end.
Point 5 has nothing. A nested sort is a scalar subquery evaluated per row of the
outer query — the one performance cliff in this module's read path, on the screen
most likely to be sorted by a joined column, and no `EXPLAIN` run against a
development dataset would show it. Nothing in the module reference distinguishes
the cost of a filter hop from the cost of a sort hop.
**If not ready:** Nothing to write by hand. One paragraph: a filter hop is an
`EXISTS` the planner can usually turn into a semi-join, a sort hop is a
correlated scalar subquery, and a list screen sorted by a related column on a big
table wants a denormalised column instead.

### H-SQLREPO-22 — The one query this cannot express
**Who:** an engineer writing the monthly reconciliation report
**Wants:** a window function and a recursive CTE, without giving up everything the repository was protecting
**Story:** The DSL cannot spell it. They are going to write SQL. The question is
what they lose by doing it, and whether the statement joins the transaction the
handler already opened.
**Must hold:**
1. A predicate the DSL cannot express can be dropped into a query that is otherwise ordinary, and everything the repository narrows still applies.
2. A whole statement can be run against the same connection the repository uses.
3. When the caller is inside a transaction, that statement is inside it too.
**Today:** 🟡 partial
**Evidence:** 1 holds and is the answer this sweep otherwise talks past.
`crud.Raw(sql, args...)` (`crud/predicate.go:480`) is a *predicate*: it renders
inside the `WHERE` the repository built, so the blueprint scope, the relation
scopes and the gate's narrowing all still AND around it, and `?` markers are
renumbered into the dialect's placeholders (`crud/predicate.go:364-385`, which
also refuses a fragment with fewer markers than arguments — a hand-written `$1`
would otherwise be renumbered against somebody else's bind). Column names are the
caller's to quote; the doc comment says so. `grep -n 'crud.Raw'
docs/modules/en/sqlrepo.md` is empty, so nobody reading this module's reference
knows it exists.
2 holds through `crud.SourceOf(repo)` (`crud/executor.go:195`), which walks the
decorator chain properly rather than type-asserting one layer down ([[D-061]]).
3 **fails, and it is squarely in this module's remit** — which database a write
lands on. A `crud.Source` is an `Executor` over the pool
(`crud/executor.go:56-59`); it does not consult the context. So a hand-written
statement run through `crud.SourceOf(users)` goes to the pool and **not** to the
transaction the handler opened, commits independently of it, survives a rollback,
and reports success. The correct incantation is `crud.ExecutorFor(ctx, src)`
first, falling back to `src` — which is exactly what the repository does for
itself at `repository.go:95-101`. `grep -n 'SourceOf\|ExecutorFor'
docs/modules/en/sqlrepo.md` is empty.
**If not ready:** Nothing to build. Three paragraphs in the module reference:
`crud.Raw` for a predicate and what it keeps; `crud.SourceOf` for a statement;
and `crud.ExecutorFor` before it, with the failure named — a report that runs
outside the caller's transaction and says nothing. A helper on the seam —
`crud.ExecutorOf(ctx, repo)` returning the executor the repository itself would
use — would remove the trap rather than documenting it, and is a few lines in
`crud`.

### H-SQLREPO-23 — Forty columns and one of them is a megabyte
**Who:** an engineer whose `documents` table has a `body` column
**Wants:** the list screen to fetch three columns, not forty
**Story:** The list endpoint selects title, author and date. Somebody later loads
one of those partial models, changes the title, and saves it.
**Must hold:**
1. Asking for a subset of columns is a query option, and the primary key comes along so the row is still addressable.
2. A projected read is distinguishable from a row whose columns are genuinely empty, or the caller is told it is not.
3. Nothing bad happens if a projected model goes back through a write.
**Today:** 🟡 partial
**Evidence:** 1 holds. `crud.Select` (`crud/options.go:157`) resolves through
`projection` (`repository.go:424-467`), which forces the primary key in for every
projection but a `DISTINCT` one — the reasoning and the exception are [[D-024]],
which is `Status: open` for the `DISTINCT` half.
2 **fails, structurally and probably unfixably.** A projected row is scanned into
the same `M`, so an unselected column holds the Go zero value and is
indistinguishable from a column that is genuinely null or empty. That is inherent
to scanning into a struct rather than a map, and it is fine — as long as it is
written down, which it is not.
3 **fails, and it is the compound of this case with H-SQLREPO-19.** A keyed
`Save` writes every non-`generated` column from the model. Load with
`crud.Select("ID", "Title")`, change the title, `Save` — and `body` is written as
the empty string. The two features are documented in sections that never mention
each other. A projected model through `Update` is safe (the DTO decides what is
written), which is the shape most callers use and is why this has not bitten yet.
There is a second, narrower collision already in the blockers: `Update` with
`crud.Select(...)` on a *versioned* model forces the key in but not the version
column, so `versionCheck(&cur)` at `repository.go:801-812` reads a zero version
and the pin renders `version = 0`, and every such update fails as stale.
**If not ready:** Nothing to build for 1. For 3, either force the version column
into every projection the way the key is forced, and say in the module reference
that a projected model must not be handed to `Save` — or make `Save` refuse a
model the repository knows was projected, which it cannot know today. The
paragraph is cheap; the refusal is not.

### H-SQLREPO-24 — "Is that email taken?" and the badge on the dashboard
**Who:** an engineer on the signup form, and one on the nav bar
**Wants:** a cheap yes/no before a create, and a number that is allowed to be a little stale
**Story:** The signup handler checks whether the address exists before inserting.
The nav bar shows an unread count that refreshes every thirty seconds.
**Must hold:**
1. Both are one call with the same options every other read takes, under the same narrowing.
2. A read whose answer decides a write is not served stale.
**Today:** 🟡 partial
**Evidence:** 1 holds. `Count` (`repository.go:559-585`, including the derived
table it needs under `DISTINCT` so the total matches the page) and `Exists`
(`:587-604`) both AND `r.scoped(o)` and carry `r.relScopes(o)`. Both are on the
public surface (`docs/modules/en/sqlrepo.md:59`).
2 is the caller's to apply and nothing tells them. Both route through
`r.read(ctx, o)` (`:584`, `:595`) with no `crud.PrimaryOnly()`, so on a
`crud.ReadWrite` source both go to the replica. For the badge that is right and
is the point. For "is this email taken, before I insert" it is [[D-032]]'s own
rule one layer out: the read decides a write, the write is the caller's, and the
option that fixes it is `crud.Exists(ctx, crud.Where(...), crud.PrimaryOnly())`
— which nothing in this module's documentation connects to that question.
It also races regardless of which database answers, which is the other half of
H-SQLREPO-05: check-then-insert is not a substitute for a unique index and a
`crud.ErrConflict`.
**If not ready:** Nothing to build. Two sentences in the module reference beside
`Count`/`Exists`: they are replica-eligible, and an `Exists` that gates a write
wants `PrimaryOnly` *and* is still not a substitute for the constraint.

### H-SQLREPO-25 — It is not a version conflict, it is a deadlock
**Who:** an engineer whose checkout handler started failing under load
**Wants:** to know whether to retry
**Story:** Two concurrent checkouts take row locks in opposite orders. One is
chosen as the victim. The handler has to tell that from "the row is gone" and
from "your data is wrong", because only one of the three is worth retrying.
**Must hold:**
1. A deadlock or a lock-wait timeout is distinguishable from an integrity violation and from a missing row, without importing a driver package.
2. There is something to compare against.
**Today:** ❌ missing (at this layer)
**Evidence:** With a bare `Bind` the caller gets the raw driver error.
`sqlfault.Wrap` (`crud/sqlfault/classify.go:137-157`) promotes only integrity
violations to a sentinel when no classifier is configured; `40001`, `40P01` and
`55P03` fall straight through untouched. This is the shape H-SQLREPO-10's
must-hold 5 makes *likely* rather than unlikely — the partial update takes
`FOR UPDATE` on its load inside a transaction (`repository.go:712-715`), which is
what produces lock ordering in the first place.
The codes exist one module over and are already mapped:
`errs/sqlerr/postgres.go:25-27` gives `55P03 → CodeLockTimeout`,
`40P01 → CodeDeadlock`, `40001 → CodeSerializationFailure`, and
`errs/codes.go:94-98` maps all three to `errs.KindRetryable`. So the answer is
"configure a classifier at the adapter". A consumer asking "is this worth
retrying?" from `crud/sqlrepo` finds nothing, and there is no `crud.ErrRetryable`
to compare against the way `crud.ErrConflict` exists for a collision.
**If not ready:** Today the handler imports the driver and matches SQLSTATEs, or
retries everything. This is not `sqlrepo`'s code to change, and it is `sqlrepo`'s
documentation to carry, because this is where the lock is taken: name the
classifier, name the kind, and — the part that is a real gap — consider a
`crud.ErrRetryable` sentinel beside `ErrConflict`, so the answer does not require
the `errs` layer. It pairs with the isolation-level knob in H-SQLREPO-10: a
serializable block without a retryable sentinel is a trap.

### H-SQLREPO-26 — One repository, many goroutines
**Who:** an engineer wiring the repository into a server for the first time
**Wants:** to know whether to build it once at start-up or once per request
**Story:** They have `var Users = sqlrepo.Define[...](...)` at package scope and
`users := Users.Bind(db)` next to it. Then a multi-tenant requirement arrives and
they wonder whether to bind per request instead.
**Must hold:**
1. A bound repository is safe to use from many goroutines at once.
2. It is stated whether `Bind` is cheap enough to call per request, and whether `Define` is.
3. If per-request binding is the way to narrow per tenant, that is written down where `Scope` is.
**Today:** 🟡 partial
**Evidence:** 1 holds in practice and is proven only indirectly: the whole
integration suite runs under `-race` against shared repositories, and the
process-global state the library holds — the schema cache and the per-handle
catalog — is what that suite exists to shake. Nothing states it as a contract.
2 has the facts and states none of them. `Bind` allocates a `repository`, asks
`crud.ReadSourceOf` once, precomputes the statement strings and wraps the
decorator chain (`blueprint.go:246-249`, `repository.go:31-62`); it issues no
statement, so per-request binding is affordable. `Define` is not: it mutates the
process-global `tableRegistry` (`crud.RegisterTable[M]`, `blueprint.go:182` →
`crud/relation.go:152`) and does the reflection, so it belongs at package scope
and calling it per request is both waste and a write to shared state.
3 **fails, and it is the answer to H-SQLREPO-18 point 7 that nobody has written.**
The honest guidance is: `Scope` is per-table and per-everyone; a per-request
narrowing is either `security.Gate` with a `Policy.Scope` that reads the context,
or a repository bound per request over a per-request blueprint — and the second
means one `Define` per tenant shape at start-up, not per request.
**If not ready:** Nothing to build. A "Lifecycle" paragraph in the module
reference: `Define` once at package scope, `Bind` as often as you like, a bound
repository is safe to share, and per-principal narrowing is the gate's job.

### H-SQLREPO-27 — The table is not in the default schema
**Who:** an engineer putting the analytics tables in their own PostgreSQL schema
**Wants:** `sqlrepo.Define[Event, int64, EventUpdate]("analytics.events")`
**Story:** The events live in `analytics`, the application tables in `public`,
and the connection's `search_path` is the default. They write the qualified name
in the declaration because that is where a table name goes.
**Must hold:**
1. Either a qualified table name works, or it is refused at declaration time with the alternative named.
**Today:** ❌ missing
**Evidence:** Neither. The table name is rendered as **one** quoted identifier:
`crud/render.go:41` is `Table() → Ident(m.Table)`, `Ident` calls `d.Quote`, and
`crud/dialect.go:70-72` wraps the whole string in double quotes. So
`"analytics.events"` is a table whose *name contains a dot*, and the first query
fails with "relation does not exist". There is no `sqlrepo.Schema(...)` setting,
nothing refuses the dot at `Define`, and `search_path` appears nowhere in
`docs/modules/en/sqlrepo.md`. H-SQLREPO-12 hands a consumer a second database for
events; the next thing that consumer does is put those events in their own
schema.
**If not ready:** Today the answer is to set `search_path` on the connection, and
nothing says so. The cheapest fix is a refusal: a table name containing a `.` is
rejected at `Define` with "set search_path on the connection, or name the schema
with sqlrepo.Schema" — which is one check and turns a confusing driver error into
an actionable one. A `Schema` setting is the larger version and touches every
render site that calls `Table()`.

## The DX this should have

### The call site

```go
//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly DeletedAt

type User struct {
    ID        int64               `db:"id,pk,auto"`
    TenantID  int64               `db:"tenant_id,immutable"`
    Email     string              `db:"email"`
    Name      string              `db:"name"`
    Age       crud.Opt[int]       `db:"age"`
    Archived  bool                `db:"archived"`
    CreatedAt time.Time           `db:"created_at,generated"`
    DeletedAt crud.Opt[time.Time] `db:"deleted_at"`
    Comments  []Comment           `rel:"has_many,fk=UserID"`
}

// vv_gen.go — generated, per [[D-018]]. It also asserts at init that the DTO
// covers every writable column, so a new column is a start-up refusal rather
// than a column PATCH silently cannot reach. `generated` and `immutable`
// columns are dropped by their own tags, so only DeletedAt needs the flag.
//
//   type UserUpdate struct { Email *string; Name *string; Age crud.Opt[int]; Archived *bool }
//   var  User_ = specs.Metamodel[User, UserAttrs]()
//   func init() { port.MustCoverUpdate[User, UserUpdate]("DeletedAt") }

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")

users := Users.Bind(crudsql.Postgres(db))

u := User{TenantID: 1, Email: "ann@x.io", Name: "Ann"}
err := users.Save(ctx, &u)                            // u.ID and u.CreatedAt filled
got, err := users.Update(ctx, u.ID, UserUpdate{Name: crud.Ptr("Anna")})
page, err := users.Get(ctx, crud.Where(crud.Eq("TenantID", 1)), crud.Limit(20))
```

This is what the code does today with one exception, marked: `crud.Ptr` does not
exist. The only `ptr` helper in the repository is unexported and in a test file
(`crud/update_test.go:141`), so every consumer writes their own, in every package
that builds a patch. Three lines in `crud` — stdlib only, so [[D-016]] and
[[D-033]] are untouched — removes a line of boilerplate from every `PATCH` call
site in the application, which is the shape that matters at the twentieth
resource rather than the first.

What this is *not* is three lines. It is an 11-line model, a generate directive,
a generated file, and two lines of vv, and it asks a newcomer to hold five
things: the `db` tag vocabulary, the three type parameters on `Define`, the
difference between `*T` and `crud.Opt[T]` and plain `T` in a DTO, that options
name model fields rather than columns, and that a Setting goes at `Define` while
a Middleware goes at `Bind`. Against a hand-written repository plus the six
handlers it feeds — `GET /users/{id}`, `GET /users`, `POST`, `PATCH`, `PUT`,
`DELETE` — that is a good trade. Against "two lines plus one", it is not the same
claim.

### Turning one knob

```go
var Users = sqlrepo.Define[User, int64, UserUpdate]("users",
    sqlrepo.SoftDelete(User_.DeletedAt.Name()),                          // exists
    sqlrepo.MaxLimit(200),                                               // exists
    sqlrepo.Scope(specs.Predicate(User_.Archived.Eq(false))),            // exists
    sqlrepo.RelationScope(User_.Comments.Path(),                         // exists — and note
        specs.Predicate(Comment_.DeletedAt.IsNull())),                   // the *target's* metamodel

    sqlrepo.ConflictOn(User_.TenantID.Name(), User_.ExternalID.Name()),  // does not exist — H-05
    sqlrepo.BatchSize(500),                                              // does not exist — H-06
    sqlrepo.VerifyAgainst(cat),                                          // does not exist — H-17
)

users := Users.Bind(crud.ReadWrite(primary, replica),   // exists
    security.Gate(policy),                              // exists — and only closes the
    faults.Enrich[User, int64]())                       // write hole when the policy
                                                        // declares Scope *and* Inspect

// exists
err := users.Tx(ctx, func(ctx context.Context) error { ... })

// does not exist — H-SQLREPO-08. A derived blueprint, not a decorator and not a
// query option. Its reads see tombstones; Restore is its only write; Delete and
// DeleteAll refuse. Today: a second Define over the same table, repeating every
// safety-relevant Setting *and* every middleware by hand — including the gate.
var Tombstones = Users.ShowingDeleted().Bind(primary, security.Gate(adminPolicy))
err = Tombstones.Restore(ctx, id)

// does not exist — H-SQLREPO-10. On both, with the refusal written once where
// the early return is: InTx returns fn(ctx) when a transaction is already
// there, so a level asked for on a transaction it is joining has to be an error
// rather than a silent drop.
err = users.Tx(ctx, fn, crud.Isolation(crud.Serializable))
err = crud.InTx(ctx, primary, fn, crud.Isolation(crud.Serializable))
```

Two names in that block cost the consumer a second wiring site rather than a
word on the declaration, and both are called out where they appear:
`ShowingDeleted` is a second blueprint with its own `Bind`, and the isolation
level is not a property of a table at all.

Two of these are cheaper than they look and three are not.

**`BatchSize` should not be a number the author supplies.** The dialect knows its
placeholder ceiling and the blueprint knows `len(fields)`, and both are in hand at
`Bind`; `crud/preload.go:17` already chunks at a constant nobody is asked for.
Keep `BatchSize(n)` as an override for somebody who measured something. But the
objection to chunking is not [[D-014]] — the boundary comes from the input length,
not a map walk, so each chunk still renders byte-identically — it is **atomicity
and cost, and the code states it**: `SaveAll` refuses a mixed batch so the call
"stays one round trip or none — silently becoming two would make the cost
invisible, which is the only reason to reach for this over a loop"
(`repository.go:1116-1119`). Chunking derived at `Bind` is precisely becoming
two, and it turns 20,000 rows from all-or-nothing into partially written when
statement 14 fails outside a transaction. So the proposal has to state the
semantics, not just the mechanism, and pick one: either a chunked `SaveAll` opens
its own transaction when none is on the context — which needs a `crud.Beginner`,
and a foreign executor handed over by an ORM is not one — or it refuses a batch
past the ceiling with an error naming the ceiling and the chunk size to use. The
second is the smaller change and the honest one. Whichever is chosen, the doc
comment at `repository.go:1116-1123` is amended in the same change, because it
currently argues against the fix.

**`VerifyAgainst` is cheap** and is lifted straight from H-SQLREPO-17's remedy: an
opt-in that runs `crud/catalog` against the model once, so the deploy-before-the-
migration case is a start-up refusal for the deployments that want one and
nothing at all for the deployments that cannot talk to the database at boot.

**`ShowingDeleted` is not as cheap as it looks, and the shape matters more than
the cost.** `resolveSoftDelete` does not isolate the fold — it is destructive
(`blueprint.go:220` rewrites `bp.set.scope` itself), and `resolveRelationScopes`
then bakes the already-folded scope into `bp.relScopes` via `ForModel` (`:236`).
So a derived blueprint cannot be "a copy with the fold dropped": the `Blueprint`
keeps the mutated `settings` and not the original `Setting` list, and a copy that
only touched `set.scope` would still hide tombstones on every self-relation hop —
a preload and a nested filter disagreeing with the root query. The derivation has
to re-run both resolutions from the original settings, which means keeping them.
It has to be a *blueprint* derivation and not `Bind(db, sqlrepo.ShowDeleted())` —
`Bind`'s variadic is `crud.Middleware[M, ID]` (`blueprint.go:246`), so anything
passed there is a decorator, the exact form [[D-031]] exists to forbid.
And its write verbs have to be spelled out, because both obvious answers collide
with a line of D-031. Keep the stamp and "Why the count is what it removed from
view" (`D-031:31-33`) stops holding: that argument is true *because* of the fold
this derivation drops, so `Tombstones.Delete(id)` would restamp a tombstone and
answer 1. Drop the stamp and one table has a soft-deleting repository and a
hard-deleting sibling, which is the "half applied" outcome "Why one setting rather
than two" (`D-031:25-29`) exists to make impossible. The shape that survives both
readings is a view that refuses `Delete` and `DeleteAll` outright and carries
`Restore` (clear the stamp) and, if anyone needs it, `Purge` (physical) instead.
That also gives `Restore` the permission of its own [[D-030]] requires, rather
than smuggling it in as the tombstone view's update. **This is a change to D-031
and must be written into it.**

**`ConflictOn` is expensive** and H-SQLREPO-05 says why: it takes a list, not a
name — real natural keys are `(tenant_id, external_id)` — it means different
things on the two engines because `ON DUPLICATE KEY UPDATE` takes no target at
all, it amends [[D-011]]'s decision table, it widens the exported `crud.Dialect`
interface (`Upsert(pk string, cols []string)`, `crud/dialect.go:18`), and it makes
`full.swallowed` (`crud/probe/plan.go:238-244`) answer the wrong question. It also
has to say which of `Save`'s two branches it changes, and the answer is both:
today the key-less branch carries no conflict clause at all
(`repository.go:619`), and giving it one means `refresh` (`:673-680`) must re-read
by the conflict target rather than by the primary key on a dialect without
`RETURNING` — the expensive part, and it belongs next to the knob rather than
three sections later. Before the tag, a day. After it, a major version.

**The isolation level** cannot reach a driver without widening a public seam.
`crud.Beginner` is `Begin(ctx) (Tx, error)` and adapters outside this repository
implement it. The shape that does not break them is a second optional interface —
`BeginTx(ctx, crud.TxOptions) (Tx, error)` — discovered the way `crud.BeginnerOf`
(`crud/executor.go:254`) already discovers the first rather than with a bare type
assertion ([[D-061]]), falling back to `Begin`. The refusal for a level asked for
on somebody else's transaction goes at `crud/executor.go:503-506`, the one early
return both call sites share. And a serializable block without something to retry
*on* is a trap: it ships with H-SQLREPO-25's `crud.ErrRetryable`, or neither
ships. None of that is `sqlrepo`'s: it is `crud` plus `crud/adapter/crudsql`.

**Not proposed, and why.** Composite keys (H-SQLREPO-01 point 8) stay refused; the
answer is a surrogate key, and the refusal already names the field. `Restore`
appears above only as part of the tombstone view, because [[D-030]] makes a new
`crud.Core` verb an obligation on every decorator and it should arrive once, with
its own permission, not twice. And the biggest item — `Scope` reaching `Save` —
is not a knob at all; it is the next section.

### Why this shape

The declaration is where a property of the *table* belongs, and most gaps in this
sweep are table properties that have nowhere to live. Which unique column a write
conflicts on is one fact about the schema, and putting it on the call site would
invite two call sites to disagree. How many rows fit in one statement is a
function of the engine and the column count and belongs to nobody.

The alternative — a builder, or an options struct threaded through every verb —
buys reach at the cost of the first ten lines, and the first ten lines are the
whole reason someone adopts this over writing the SQL. The `Setting` list already
proves the pattern: `SoftDelete` is one word and it turns on a read filter, a
write rewrite and a validation, and it does that *because* it is a declaration and
not a decorator ([[D-031]]).

Three things about the Setting list have to change for it to keep that promise.

First, every setting has to be validated at `Define`. Today `SoftDelete` and
`RelationScope` are and `Scope` and `DefaultSort` are not, so half of a settings
list refuses a typo at boot and the other half refuses it on the first request.

Second, each new knob takes a *name*, and the module reference already pushes the
metamodel spelling for exactly these declarations, because a rename otherwise
leaves a declaration that still compiles, still reads as protection and narrows
nothing. The knob block above is written that way deliberately, including the
part that is easy to get wrong: a `RelationScope`'s predicate is written against
the **target** model, so it takes `Comment_`, not `User_`, on the line directly
below one that takes `User_`.

Third, and this is the one that cannot be fixed by adding a knob: **a declaration
that narrows reads and deletes but not writes is not a "repository property" in
the sense a consumer reads it as.** Three options, cheapest first.

- **Rename it.** `sqlrepo.ReadScope(pred)` says what it does. Zero mechanism, zero
  dialect work, no change to [[D-011]], and it costs every existing consumer one
  find-and-replace. A worse feature name and a much better release.
- **Refuse the combination.** When a blueprint declares `Scope`, refuse `Save` and
  `SaveAll` unless the declaration also carries an explicit opt-out
  (`sqlrepo.UnguardedWrites()`). It cannot be conditioned on the middleware list:
  `crud.Middleware[M, ID]` is `func(Core) Core` (`crud/repo.go:58`), an opaque
  function, so `Bind` cannot tell a gate from a logger. A start-up error plus one
  explicit opt-out turns "an unprotected write surface they believe is protected"
  into a decision somebody made on purpose.
- **Make the write narrow.** A conflict `WHERE` on the upsert, which PostgreSQL
  supports (`ON CONFLICT … DO UPDATE … WHERE`) and MySQL does not, so it is a
  dialect split and a change to [[D-011]]'s table. It closes the overwrite half
  and not the insert half — an `INSERT` with a foreign tenant id still inserts —
  so on its own it is not enough either.

Whichever is chosen, the Settings documentation has to say which verbs each
setting reaches, verb by verb, and the line at `docs/modules/en/sqlrepo.md:107`
has to stop saying "every scoped write".

### What it must not break

- **[[D-011]]** — `Save` is one method and takes no options. `ConflictOn` as a
  *Setting* respects that shape, but it **amends D-011's decision table**, which
  spells the conflict target as `(pk)`. A narrowing `WHERE` on the upsert amends
  it too. Both are changes to the decision and must be written into it, not
  around it.
- **[[D-014]]** — the SQL is deterministic. A chunked batch still renders a
  byte-identical statement for a given chunk, because the boundary comes from the
  input length, not from a map walk. D-014 is not the objection to chunking;
  atomicity is.
- **[[D-031]]** — soft delete is a statement, not a decorator, so "show the
  tombstones" cannot be a decorator and must not be a *query option*: an option
  would be exactly the composability the decision exists to deny. A
  blueprint-derived second declaration is the only form that respects it. **The
  tombstone view is a challenge to two of D-031's stated reasons** — "Why the
  count is what it removed from view" and "Why one setting rather than two" — and
  the resolution proposed above (the view refuses `Delete`/`DeleteAll` and carries
  `Restore`) has to be written into D-031 rather than implemented around it. A
  `crud.IncludeDeleted()` option would be a direct challenge to D-031's core and
  this sweep does not propose one.
- **[[D-030]]** — `Restore` is not a free verb. Every addition to `crud.Core` is
  an obligation on `security.gate`, enforced by `coreVerbs` in
  `crud/decorators/security/obligation_test.go`, and `Restore` needs a permission
  of its own rather than riding on update.
- **[[D-032]]** — a replica never decides a write. H-SQLREPO-09 is that invariant
  being broken, not challenged; the fix is the decision doing its job, and it
  drags UC-009's status and the Index with it. H-SQLREPO-24 is the same rule one
  layer out, where it is the caller's to apply and nobody says so.
- **[[D-029]]** — an aggregate is on the seam and not on the wire. Defaulting
  `Aggregate` to unpaged is safe *because* of that: no client composes the
  grouping, so the row count is bounded by the cardinality of columns the
  application chose.
- **[[D-018]]** — the update DTO is generated. The tombstone exclusion is
  `-readonly`, declared at generation time, and `port.MustCoverUpdate` makes that
  the only way to spell it. Note that `-readonly` is a no-op for a column the
  model's own tags already drop (`internal/codegen/codegen.go:46`, `:302-310`), so
  it names only `DeletedAt` here.
- **[[D-026]]** — `Status: open`. H-SQLREPO-07 point 7 is its option 3, and it is
  the half of that decision that lives in this module.
- **[[D-024]]** — `Status: open`. H-SQLREPO-23 is the happy-path half of it: a
  projection forces the key in except under `DISTINCT`, and it does not force the
  version column in at all.
- **[[D-010]]** — `Update` writes only what differs, so the diff has to see the
  row it is diffing against. That is why a projected load and a version column
  cannot both be honoured.
- **[[D-061]]** — an optional interface is never found with a bare type
  assertion. A `BeginTx` seam is discovered through `crud.BeginnerOf`, and
  `crud.SourceOf` is what H-SQLREPO-22's escape hatch has to use.
- **[[D-007]]** — a narrowing does not cross a model boundary on its own.
  H-SQLREPO-16 point 6 is that invariant working as designed and reading, to a
  consumer, like a leak. It is a documentation obligation, not a code change.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| Model, DTO, `Define`, `Bind` — and the surface exists | Exactly that: an 11-line model, a generate directive, 2 lines of vv, 5 concepts to hold, and one `ptr` helper every consumer writes themselves | none |
| A broken declaration stops the process | For tags, ID types and the DTO, yes. For `Scope` and `DefaultSort`, no — a typo is an `UnknownFieldError` on the first request | small |
| A Go-only field on the model | Silently becomes a column. `db:"-"` is the answer and nothing points at it | small (docs) |
| `PATCH` that tells absent from null | Exactly that, no ceremony | none |
| Filter, sort, page, total in one call | Exactly that, plus cursors. `SkipTotal` is the answer to what the total costs and is named nowhere a consumer reads | none + small (docs) |
| An `Unpaged()` request against a repository with `MaxLimit` | 50 rows, `Total: 50`, `TotalPages: 1`, `HasNext: false`. A truncated page reporting itself as the whole answer, reachable from `?all=true` | small (report the real count, or refuse `Unpaged` when `MaxLimit` is set) |
| "Give me everything" meaning one thing | `Get(Unpaged())` truncates at `MaxLimit`; `GetAll()` returns the table. Both silent, both deliberate, neither written down | small (docs) |
| A search box that matches the same rows on both engines | `Contains` is `LIKE` — case-sensitive on PostgreSQL, insensitive on MySQL. `LikeIgnoreCase` is portable and unindexable. Neither fact is anywhere | small (docs) + a false comment in `crud/query` |
| Relations without N+1 on the read path | `crud.Preload(...)`, chunked, depth-capped, and the far-side narrowing declarable | none |
| A preload of a to-many a client can widen | Unbounded rows in one statement. A per-relation limit is refused by design; the answer is a second query and nothing says so | medium (docs, or a documented second-query idiom) |
| A child's own `SoftDelete` surviving a preload from elsewhere | It does not. The same fact is declared twice, on two blueprints, with nothing checking they agree | small (docs) or medium (a registry the preloader consults) |
| Filter and sort through a relation | `crud.Where(crud.Eq("Customer.Country", "DE"))` and the sort likewise, one statement, scopes carried | none |
| Relations on the write path | `Save(order)` writes the order and silently ignores `order.Items`. No cascade, no refusal, no sentence anywhere | small (docs) or large (a cascade) |
| "Update this object" with a half-filled model | A keyed `Save` writes every writable column from the model. `email` cleared, `archived` reset, nil returned | small (docs) — and it is the highest-value paragraph in the module |
| A create refused by a unique index | `crud.ErrConflict` from a bare `Bind` on every adapter; the `errs.Code` needs an engine-named source; the field name needs `faults.Enrich` and a catalog. None of the three boundaries is stated here | small (docs) |
| Not fetching the fat column | `crud.Select(...)`, key forced in. A projected model handed back to `Save` is silent data loss, and the two features are documented apart | small (docs) |
| Soft delete as one word on the declaration | Exactly that — for reads and deletes | none |
| A `Scope` that means what it says | Reads, deletes and the load half of a patch. Not `Save`, not `SaveAll`, not the read-back. The gate closes it only when the policy declares both `Scope` and `Inspect` | large — or small, if the answer is `ReadScope` |
| A narrowing that depends on who is asking | Not expressible as a Setting: `Scope` is a fixed predicate on a package-level blueprint. That is the gate's job and it is written down nowhere | small (docs) |
| Seeing the tombstones | A second `Define` over the same table — this repository's own idiom, undocumented. It costs repeating every safety-relevant Setting *and* every middleware, and its `Delete` is physical | small |
| Restoring one | No verb. `Save` does it by accident, which is the bug in H-SQLREPO-08 | large |
| The tombstone column frozen against the DTO | Not frozen. `-readonly DeletedAt` at generation time, or a `PATCH` deletes rows through the update permission | small |
| A dashboard that returns every group | `crud.Unpaged()`, which nobody is told to pass, and which on this one verb also discards `MaxLimit` | small (one default) |
| A filtered write that refuses what it cannot do | Accepts `Limit` and ignores it. [[D-026]] option 3 | small |
| Upsert on a natural key | Nothing. `Exists`-then-`Save` — a race, and a replica read — or a hand-written statement | large |
| A batch that survives a real import | A hand-written chunking loop at a size the author guesses, partitioned by key presence, and on MySQL the keys assigned by hand | large |
| The query the DSL cannot express | `crud.Raw` keeps every narrowing; `crud.SourceOf` runs a whole statement and leaves the caller's transaction unless they ask `crud.ExecutorFor` first. Neither is documented here | small (docs) + small (a helper) |
| Knowing whether to retry | Nothing. A deadlock is the driver's error; the codes exist in `errs` and there is no `crud.ErrRetryable` | medium |
| One transaction at a chosen isolation level | A second datasource with `WithTxOptions`, or open it with the driver and hand it over — the short path abandoned. Owned by `crud` | large (cross-reference) |
| A composite-key table | Refused, with the field named. A surrogate key is the answer | large, and not proposed |
| A table in a non-default schema | `"analytics.events"` becomes one quoted identifier and the first query fails. No setting, no refusal, no `search_path` note | small |
| A read that decides a write staying on the primary | True everywhere the tests look, false at `repository.go:821` | small (one line) |
| Knowing the model matches the table | Nothing. A drifted column is a driver error on every read of that table | large |

**Overall:** For reading one row and patching one row this is finished work, and
it reads that way — the shortest thing that works is genuinely the shortest thing,
and the comments in those paths name the bug each shape came from rather than
restating the code. Everything else is thinner than the declaration makes it look,
and it is thinnest in one direction: the model going *back*. The permanent
narrowing that reads honour is in no write statement; a keyed `Save` is a full
row replacement that nothing at the call site distinguishes from a patch;
relations travel one way; the batch verb has a ceiling nobody wrote down. The
read path is not finished either — two of its verbs truncate silently and report
the truncation as the whole answer, which is the same defect this sweep rates
serious when it finds it in `Aggregate`. The other place customising means
starting over rather than extending is the transaction seam: joining somebody's
transaction is one line, and choosing an isolation level means binding a different
repository. The good news is that most of the distance in the table above is
documentation, and the two most valuable paragraphs in it — what a keyed `Save`
writes, and that associations are yours — cost nothing but a decision to write
them.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `sqlrepo.Scope` does not reach `Save` or `SaveAll`, and the read-back passes `within = nil` (`repository.go:649`, `:667`, `:1152`) | blocker | A repository declared for tenant isolation has protected reads and an unprotected upsert. `security.Gate` closes the overwrite half only with `Policy.Scope` and the insert half only with `Policy.Inspect` (`security.go:499-501`, `:536-537`), so a scope-only policy leaves foreign-tenant inserts open. `TestScopeCannotReachSave` pins the insert half; **the cross-tenant overwrite has no test anywhere** |
| 2 | A `Save` carrying a tombstone's key resurrects the row and reads it back as a success (`crud/meta.go:289-299` → `repository.go:59`, `:667`) | blocker | Soft delete's read half holds and its write half does not. Reachable through the ordinary create endpoint for any client-owned key (`port/service.go:152-167`) and through `PUT` (`:190-211`). The gate makes it worse: `saveTarget`'s existence probe runs through the scope that hides the tombstone (`security.go:541`), so the write is authorised as a fresh create. Open tension 17; UC-016 says so in words; no control test |
| 3 | A keyed `Save` writes every writable column from the model, and nothing says so (`repository.go:623` → `crud/meta.go:287-299`) | blocker | "Set the id, change a field, save it" is the habit every ORM refugee brings, and it clears every column the model left at its zero value. Same data-loss shape [[UC-003]] exists to prevent, one verb over, reachable from `PUT /{id}`. The fix is a paragraph; the absence of the paragraph is the blocker |
| 4 | Relations on a model are never written and never refused (`crud/meta.go:102-104`, `repository.go:631`) | serious | `Save(order)` with `order.Items` populated persists the order, returns nil, and the children do not exist. No cascade anywhere in `crud/`, and no sentence in the module reference, [[D-017]] or this module's docs. For an aggregate root that is the most common write in the application |
| 5 | `SaveAll` builds one statement with a placeholder per value and never chunks (`repository.go:1156-1176`); `Delete(ids...)` does the same (`:882`) | serious | The only bulk-write verb has an undocumented row ceiling below any real import, and crossing it is a driver error naming nothing about vv. One defect, one fix, two call sites. The library already chunks at 900 in the preloader for this exact reason |
| 6 | `missedRow`'s existence check (`repository.go:821` → `Exists` at `:595`) routes to the replica; its answer decides `ErrNotFound` versus `ErrStaleVersion` | serious | Breaks [[D-032]]'s written invariant with no test, and `missedRow` is absent from that decision's "Where it lives" list. Under lag a lost update is reported 404 — "give up" — for an edit that should have been retried, and a deleted row is retried forever. One line, plus fixing UC-009's "covered" status |
| 7 | `Aggregate` applies the default page limit of 20 to group rows, with no total and no `HasNext` (`repository.go:1051-1055`) | serious | A dashboard silently loses groups past the twentieth and cannot tell. `crud.Unpaged()` fixes it, nothing tells anyone to pass it, and on this verb alone `Unpaged` also discards the declared `MaxLimit` (`:1052-1054` versus `crud/options.go:238-247`) — one option word with two meanings on one repository |
| 8 | `Get(crud.Unpaged())` against a repository with `MaxLimit` returns a truncated page reporting itself as the whole answer (`repository.go:217-218`, `crud/page.go:33`, `:38`) | serious | Same silent-truncation shape as row 7, in the read path. Reachable from the wire as `?unpaged=true` / `?all=true` under `AllowUnpaged` — the flag an export endpoint turns on. The pinning test asserts only that a `LIMIT` was emitted, never the response |
| 9 | `SaveAll` reads nothing back on a dialect without `RETURNING`, `generated` columns included (`repository.go:1211-1216`) | serious | The *keys* are a documented, pinned refusal (`:1120-1123`, `saveall_test.go:106-116`) and should not be re-litigated. The rest is not: an assigned-key batch on MySQL comes home with no `created_at`, while a single `Save` of the same row has it. And **no use case covers `SaveAll` at all** — [[UC-008]]'s "Out of scope" opens with "Bulk insert" |
| 10 | The soft-delete column is not frozen against the update DTO (`crud/update.go:112-120`) | serious | `PATCH {"deletedAt":"..."}` deletes a row through the *update* permission — the outcome [[D-031]]'s "Why" names as the thing it chose against. `port.MustCoverUpdate` pushes the column back in unless `-readonly` is declared at generation time |
| 11 | No conflict target but the primary key (`repository.go:59`), and the key-less branch carries no conflict clause at all (`:619`) | serious | "Insert or update, keyed on the email" has no in-library answer, and the substitute is a read-then-write race — whose read is also replica-eligible — or a hand-written statement. Closing it amends [[D-011]] and widens the exported `crud.Dialect`: cheap before the tag |
| 12 | `UpdateAll` and `DeleteAll` accept a `Limit` and emit none (`repository.go:834-871`, `:903-918`) | serious | A filtered write silently does more than it was asked. Under the gate it is [[D-026]] — `Status: open` — where `Inspect` sees ten rows and the write takes every match. Option 3 of that decision is this module's to implement |
| 13 | Three false statements in the module reference (`docs/modules/en/sqlrepo.md:73-74`, `:92-93`, `:107`) | serious | "`SaveAll` … reads every key back in order" is contradicted by the library's own doc comment and a control test. "`UpdateAll` … neither diffs nor advances a version column" is contradicted by `repository.go:857-859` and [[UC-008]] guarantee 8. "`Scope` … ANDed into every read and every scoped write" is the exact belief row 1 says a consumer must not form. A wrong option description in a consumer's reference is a bug they hit and the author did not |
| 14 | `Scope` and `DefaultSort` are stored unvalidated at `Define` (`blueprint.go:85`, `:158-196`) | sharp edge | A typo in the setting that *is* the safety boundary is a 500 on live traffic, not a start-up panic — while `SoftDelete` and `RelationScope` in the same list are validated. One function, walking the predicate the way `resolveRelationScopes` walks paths |
| 15 | Nothing checks the model against the live table; `crud/catalog` is never reached from here | sharp edge | Deploying before the migration breaks every endpoint on that table with a driver error, and H-SQLREPO-01's start-up promise reads as if it were covered |
| 16 | `crud.Contains` renders a plain `LIKE` — case-sensitive on PostgreSQL, insensitive on MySQL (`crud/predicate.go:436`, `:255-274`) | sharp edge | The same search box returns different rows on the two engines this module elsewhere insists agree. The portable spelling is unindexable. The wire's free-text search takes the same path under a comment claiming it is case-insensitive (`crud/query/compile.go:565`) — that comment is `crud/query`'s to fix |
| 17 | A to-many preload has no row ceiling and cannot be given one (`crud/preload.go:193-196`) | sharp edge | `?limit=50&preload=comments` loads every comment of 50 articles into memory. The refusal is deliberate and right — a `LIMIT` on a batched preload truncates some parents and not others — and the consumer's fix is a second query, which nothing tells them |
| 18 | A target model's own `SoftDelete` does not follow a preload from another repository (`repository.go:531-536`) | sharp edge | The consumer declares the tombstone filter on the `Comment` blueprint, preloads comments from articles, and gets tombstones. The fix is `RelationScope` on the *article* blueprint — the same fact in two files with nothing checking they agree |
| 19 | A qualified table name becomes one quoted identifier (`crud/render.go:41` → `crud/dialect.go:70-72`) | sharp edge | `Define[...]("analytics.events")` produces `"analytics.events"` and the first query fails with "relation does not exist". No setting, no refusal at `Define`, no `search_path` note anywhere |
| 20 | A statement run through `crud.SourceOf(repo)` leaves the caller's transaction and reports success (`crud/executor.go:56-59`, `:195`) | sharp edge | The escape hatch for a report the DSL cannot express commits outside the transaction the handler opened and survives its rollback. The fix at the call site is `crud.ExecutorFor(ctx, src)` first — what the repository does for itself at `repository.go:95-101` — and nothing documents either |
| 21 | A non-integer `auto` primary key on a dialect without `RETURNING` (`repository.go:656`, `:667`) | sharp edge | With `db:"id,pk,auto"` on a uuid and `DEFAULT (UUID())` on MySQL: the insert succeeds, `LastInsertId` is 0, the model keeps its zero key and the read-back answers `ErrNotFound` — a successful write reported as a missing row. Refusable at `Bind` |
| 22 | `Update` with `crud.Select(...)` on a versioned model (`repository.go:424-467`, `:801-812`) | sharp edge | `projection` forces the primary key in but not the version column, so the pin renders `version = 0` and every such update fails as stale. The neighbouring case is worse and quieter: a projected model handed to `Save` writes the unselected columns as zeroes |
| 23 | No `crud.ErrRetryable`, and no classifier by default for `40001` / `40P01` / `55P03` (`crud/sqlfault/classify.go:137-157`) | sharp edge | The lock that makes deadlocks likely is taken in this module (`repository.go:712-715`). The codes exist in `errs/sqlerr` and map to `KindRetryable`, so the answer is "configure a classifier" — but from `crud/sqlrepo` there is nothing to compare against and nothing that says where to look |
| 24 | `SaveAll` and `Aggregate` have no unit test in `crud/sqlrepo` | sharp edge | Every claim about the batch statement and the aggregate statement is proven only behind the `integration` tag, and the natural place to pin a chunk boundary or a paging default — `crudtest`, per [[D-014]] — does not exist for either |
| 25 | `Count` and `Exists` are replica-eligible with nothing said (`repository.go:584`, `:595`) | sharp edge | Right for a badge count, wrong for the `Exists`-before-create this sweep tells consumers to write today. [[D-032]]'s rule one layer out, where it is the caller's to apply |
| 26 | The module reference's remedy for "a column DEFAULT does not fire" names `BeforeSave`, which does not exist at this layer (`docs/modules/en/sqlrepo.md:283`) | sharp edge | A service method or background job has no such seam; the only one is a `crud.Middleware`. A reference that names a hook a consumer cannot reach is worse than one that says nothing |
| 27 | `settings.replica` (`blueprint.go:33`) is declared and never read | sharp edge | Nothing at run time, but the next agent reads a settings field as a setting that exists |
| — | No isolation level on `Tx`; `WithExecutorFor` keyed on a transaction matches no repository | cross-reference | Both are owned by `crud` + `crud/adapter/crudsql` (`crud/executor.go:52`, `:335`; `crudsql.go:173`) and belong in that sweep's blocker table. Listed here without a number so the same two defects are not counted twice across two documents |

## Contested

- **"A bare `Bind` hands the caller a raw driver error when a unique index
  refuses"** (round 1, consumer lens). Challenged and not adopted, and it survived
  a second round. The classification is in the *adapter*:
  `crudsql.Executor.conflict` wraps every `Exec` and `Query`
  (`crud/adapter/crudsql/crudsql.go:91`, `:108`), and `sqlfault.Wrap` returns
  `crud.ErrConflict` on SQLSTATE class 23 with no classifier configured
  (`crud/sqlfault/classify.go:137-157`), proven on a plain `EgConses.Bind(tg.src)`
  with no decorator against five sources
  (`test/integration/dialect_edge_test.go:747`). What *was* adopted this round is
  the reviewer's correction one level down: the `errs.Code` does **not** survive a
  source built with `crudsql.From`, which is how ent is reached, and the cited
  test asserts exactly that (`:806-812`, `edge_test.go:333-352`). H-SQLREPO-14 now
  says so and has been reduced to naming the three-layer boundary rather than
  re-proving another module's work.
- **H-SQLREPO-07's partial rating.** Two reviewers in round 1 said the
  limit-versus-write mismatch belongs to `crud/decorators/security` and this case
  should be rated ready. Kept as partial, on the narrower point: `UpdateAll` and
  `DeleteAll` accept a paging option and silently ignore it *without any decorator
  present*, which is this module's own defect and [[D-026]]'s own option 3. The
  gate-side severity is cross-referenced, not restated.
- **H-SQLREPO-14 kept as a case rather than folded into one must-hold.** A
  reviewer argued it is mostly `crud/adapter/crudsql` and `crud/sqlfault`, which
  is true. Kept because it is where the consumer meets the question and because
  the boundary between the three layers is stated nowhere else; reduced to a
  single must-hold about that boundary, so the evidence is a map rather than a
  duplicate of another sweep's proof.
- **H-SQLREPO-12 kept rather than folded into H-SQLREPO-10.** A reviewer asked for
  it to be dissolved. Kept as two must-holds and a pointer, because
  `repository.go:95-101` is a real guarantee this module makes — a repository
  leaves somebody else's database alone — and folding it into the transaction case
  would bury it. Its `crud/executor.go` half is now a cross-reference and is out
  of the numbered blocker table.
- **"Raw SQL runs outside the scope, the relation scopes and the gate."** Round 1
  used this line to size three severities. A reviewer showed it is false for
  `crud.Raw`, which is a predicate rendered inside the `WHERE` the repository
  built (`crud/predicate.go:480`, `:364-385`). Adopted: the claim is now narrowed
  to a whole hand-written statement, and H-SQLREPO-22 exists to describe both
  escape hatches and the transaction trap on the second one.
- **The chunking proposal's objection.** Round 1 defended automatic chunking
  against [[D-014]] only. A reviewer pointed out the real objection is atomicity
  and that `repository.go:1116-1119` states it. Adopted, and the proposal now
  picks a semantics rather than only a mechanism — with the doc comment amended in
  the same change, because it currently argues against the fix.

## Edge cases

### E-SQLREPO-01 — An empty selection never reaches the database
**Shape:** boundary
**Setup:** A cleanup screen submits no selected IDs, or an import produces no rows.
**What the consumer does:** It passes the empty slice to `Delete` or `SaveAll` rather than branching around both calls.
**What must happen:** Both calls succeed with no statement. An empty selection must not become an unscoped write.
**Today:** 🟡 partial
**Evidence:** `Delete` returns `0, nil` before building SQL at `crud/sqlrepo/repository.go:873-875`, pinned by `TestDeleteNothingIsANoop` at `crud/sqlrepo/repository_test.go:486-494`. `SaveAll` has the corresponding early return at `crud/sqlrepo/repository.go:1124-1126`, but no `crud/sqlrepo` test covers it.
**Blast radius:** none

### E-SQLREPO-02 — A bad caller value is rejected before a write
**Shape:** misuse
**Setup:** A handler bug passes a nil model or nil update DTO into the repository.
**What the consumer does:** It calls `Save`, `SaveAll`, `Update`, or `UpdateAll` with that bad value.
**What must happen:** The call returns an actionable error without executing any statement. Bad request plumbing must not become a database round trip.
**Today:** 🟡 partial
**Evidence:** `Save` and `SaveAll` reject nil models before any executor call at `crud/sqlrepo/repository.go:609-612` and `:1129-1132`; `UpdateAll` validates its DTO before SQL at `:834-843`. `Update` instead loads the row at `:716` before `plan.Changes` rejects a nil DTO at `:725-728`. `crud.UpdatePlan` has a generic nil-DTO test at `crud/edge_test.go:382-422`, but no sqlrepo entry-point test pins the no-statement contract.
**Blast radius:** confusing error

### E-SQLREPO-03 — A conditional setting does not turn `TryDefine` into a panic
**Shape:** degenerate declaration
**Setup:** A configuration helper returns a nil `sqlrepo.Setting` when an optional feature is disabled.
**What the consumer does:** It passes that setting to `TryDefine`, the API the module reference presents as the non-panicking form.
**What must happen:** `TryDefine` returns a declaration error that names the bad setting. It must not panic after a caller selected the error-returning API.
**Today:** ❌ wrong or unhandled
**Evidence:** `Setting` is a function type at `crud/sqlrepo/blueprint.go:28-29`; `TryDefine` invokes every option without a nil check at `:183-185`. `docs/modules/en/sqlrepo.md:41` says `TryDefine` is the error-returning alternative, and no test covers a nil setting.
**Blast radius:** crash

### E-SQLREPO-04 — A missing datasource is refused at wiring time
**Shape:** degenerate declaration
**Setup:** Dependency injection supplies a nil `crud.Source` during process setup.
**What the consumer does:** It calls `Users.Bind(src)` and expects the startup failure to identify the missing binding.
**What must happen:** Binding refuses with an actionable error before the service can start. A configuration error must not be a nil-interface panic.
**Today:** ❌ wrong or unhandled
**Evidence:** `Bind` passes its source directly to `newRepository` at `crud/sqlrepo/blueprint.go:246-248`; `newRepository` immediately calls `src.Dialect()` at `crud/sqlrepo/repository.go:31-32`. No nil-source check or test was found.
**Blast radius:** crash

### E-SQLREPO-05 — Two permanent table guards both remain true
**Shape:** misuse
**Setup:** Base configuration narrows a repository to live rows and service configuration adds a tenant scope.
**What the consumer does:** It passes two `sqlrepo.Scope` settings, expecting both declarations to remain a permanent narrowing.
**What must happen:** The guards compose by AND, or the declaration refuses the duplicate. A later safety setting must not erase an earlier one.
**Today:** ❌ wrong or unhandled
**Evidence:** `Scope` assigns one `settings.scope` field at `crud/sqlrepo/blueprint.go:68-85`; `TryDefine` applies settings left to right at `:183-185`; reads use only that final field at `crud/sqlrepo/repository.go:286-292`. No test covers two `Scope` settings. This conflicts with [[D-004]], whose invariant is that a narrowing never replaces another narrowing.
**Blast radius:** data leak

### E-SQLREPO-06 — Two guards on the same relation both remain true
**Shape:** seam
**Setup:** One declaration narrows `Comments` to a tenant and another narrows the same path to visible comments.
**What the consumer does:** It passes two `sqlrepo.RelationScope("Comments", ...)` settings, expecting both guarantees to apply to a preload and a relation filter.
**What must happen:** The guards compose by AND, or the declaration refuses the duplicate path. Repeating a relation guard must not widen the far-side query.
**Today:** ❌ wrong or unhandled
**Evidence:** `RelationScope` appends declarations at `crud/sqlrepo/blueprint.go:115-134`, then `resolveRelationScopes` repeatedly calls `AtPath` at `:228-236`. `RelationScopes.AtPath` overwrites its path map entry at `crud/scope.go:43-52`; only the separate blueprint/request merge ANDs same-path scopes at `:107-138`. No local test covers two blueprint relation scopes on one path.
**Blast radius:** data leak

### E-SQLREPO-07 — Two first requests may cross one relation safely
**Shape:** concurrency
**Setup:** Two requests are the first in the process to preload the same relation.
**What the consumer does:** It shares the repository normally and sends both requests at once.
**What must happen:** Both requests resolve the same relation and neither races on process-shared metadata.
**Today:** ✅ handled
**Evidence:** `repository.preload` reaches the shared relation path at `crud/sqlrepo/repository.go:531-536`. `Relation.resolveDefaults` protects its lazy writes with `sync.Once` at `crud/relation.go:300-317`, and `TestConcurrentFirstUseOfARelationDoesNotRace` at `crud/relation_test.go:474-522` asserts 32 concurrent first resolutions agree.
**Blast radius:** none

### E-SQLREPO-08 — A timeout after the write is not mistaken for an uncommitted write
**Shape:** partial failure
**Setup:** On a dialect without `RETURNING`, the INSERT succeeds but the request context expires before the refresh query can return.
**What the consumer does:** It calls `Save` and receives the timeout error.
**What must happen:** The post-write durability state is documented as indeterminate so retry code does not assume the row was absent and create a second effect. This case is only about commit-state ambiguity after a repository write, not a general cancellation policy.
**Today:** 🟡 partial
**Evidence:** The non-`RETURNING` branch executes the write at `crud/sqlrepo/repository.go:652-660` and then refreshes the model with the same context at `:661-667`; a refresh failure is returned by `:684-695`. The error is propagated, but no test or module documentation states that a timed-out call may already have committed its write.
**Blast radius:** confusing error

### E-SQLREPO-09 — A foreign executor's `RETURNING` stream has a stated conformance contract
**Shape:** adversarial input
**Setup:** A custom source reports fewer or more `RETURNING` rows than the slice supplied to `SaveAll`.
**What the consumer does:** It uses a foreign or test-double executor and needs to know whether malformed result cardinality is a supported adversarial input or outside the executor contract.
**What must happen:** The framework must either specify and test exact returned-cardinality validation for foreign executors, or state that an executor returning successful malformed `RETURNING` rows violates its own contract. It must not present the latter as an ordinary supported-adapter release failure.
**Today:** ❓ unverified
**Evidence:** The loop stops when either stream or model slice ends (`crud/sqlrepo/repository.go:1188-1204`), so no local check establishes exact cardinality. The integration key-order test exercises normal adapters (`test/integration/saveall_test.go:89-127`), not an adversarial foreign executor; `crud.Executor` itself exposes only `Query` and `Rows` rather than a `RETURNING` cardinality guarantee (`crud/executor.go:36-39`, `:13-25`).
**Blast radius:** confusing error

### E-SQLREPO-10 — A repeated key in one batch has one stated outcome
**Shape:** concurrency
**Setup:** Two workers contribute assigned-key rows to one batch and the same primary key appears twice.
**What the consumer does:** It calls `SaveAll` instead of deduplicating separately, because the method is presented as batched `Save`.
**What must happen:** The batch is refused before SQL, or its cross-dialect outcome is stated and tested. A duplicate in one payload must not depend on an engine surprise.
**Today:** ❓ unverified
**Evidence:** `SaveAll` checks only whether each row has a key and whether the batch mixes generated and assigned keys at `crud/sqlrepo/repository.go:1128-1149`; it then builds one insert with the upsert tail at `:1151-1175`. No primary-key-duplicate case was found in `crud/sqlrepo` or `test/integration/saveall_test.go`; the duplicate-email probe case is a different constraint path at `test/integration/probe_test.go:789-795`.
**Blast radius:** confusing error

### E-SQLREPO-11 — The largest finite page does not become an unlimited read
**Shape:** scale
**Setup:** A service-side caller requests `crud.Limit(math.MaxInt)` with `crud.SkipTotal()`, or configures that value as its default limit.
**What the consumer does:** It asks for the largest representable finite page and expects the repository to retain a limit.
**What must happen:** The one-extra-row probe is saturated or refused. Arithmetic at the page boundary must not remove the limit and load the whole table.
**Today:** ❌ wrong or unhandled
**Evidence:** `Get` computes the probe as `limit + 1` at `crud/sqlrepo/repository.go:171-177`. `Options.Resolved` accepts any positive limit at `crud/options.go:241-276`, and `SQL.LimitOffset` emits a limit only when it is positive at `crud/render.go:104-110`; integer overflow therefore makes the probe negative and emits no `LIMIT`. No max-int probe test was found.
**Blast radius:** crash

### E-SQLREPO-12 — A negative maximum does not disable a safety cap
**Shape:** boundary
**Setup:** A configuration typo supplies `sqlrepo.MaxLimit(-1)`.
**What the consumer does:** It expects a malformed cap to fail at startup or remain restrictive.
**What must happen:** The declaration refuses the value. A setting named `MaxLimit` must not quietly remove the only ceiling on a caller's page size.
**Today:** ❌ wrong or unhandled
**Evidence:** `MaxLimit` stores its value unchanged at `crud/sqlrepo/blueprint.go:53-54`; `Options.Resolved` caps only when `maxLimit > 0` at `crud/options.go:241-254`. The reference says only zero disables the cap at `docs/modules/en/sqlrepo.md:104`, and no negative-cap test was found.
**Blast radius:** crash

### E-SQLREPO-13 — Middleware order matches the binding declaration
**Shape:** seam
**Setup:** A repository is bound with a rejecting outer policy followed by an observing decorator.
**What the consumer does:** It lists the policy first and relies on it to refuse before the observer and SQL run.
**What must happen:** The first middleware is outermost, as the binding documentation says, so order remains a consumer-visible safety choice.
**Today:** ✅ handled
**Evidence:** `Bind` sends its middleware through `crud.Chain` at `crud/sqlrepo/blueprint.go:244-249`; `Chain` walks the list backward, leaving element zero outermost, at `crud/repo.go:109-115`. `TestDecorateStacksWithTheFirstMiddlewareOutermost` at `crud/decorate_test.go:84-112` pins both orders and their statement counts.
**Blast radius:** none

### E-SQLREPO-14 — A stale full-model Save or Replace must not silently win
**Shape:** concurrency | seam
**Setup:** Two callers hold version 0 of a versioned row. One changes it through `Update`, then the other sends the older full model through `Save` or HTTP `Replace`.
**What the consumer does:** They use a `version` tag expecting any write carrying the stale model to refuse with `ErrStaleVersion`, rather than overwrite the newer fields.
**What must happen:** A versioned full-model write either carries a version predicate and refuses the stale row, or `Save`/`Replace` explicitly refuse versioned full replacements. It must not advertise optimistic locking while one ordinary full-write route bypasses it.
**Today:** ❌ wrong or unhandled
**Evidence:** `Save` constructs a keyed upsert from `insertFull + upsertTail` with no version predicate (`crud/sqlrepo/repository.go:609-624`); `newRepository` builds that tail from `Meta.Update` (`:52-60`), and version columns are deliberately omitted from the update list (`crud/meta.go:289-299`). `Update` alone builds `version = version + 1` and checks the prior value (`crud/sqlrepo/repository.go:733-751`, `:799-828`). `DefaultService.Replace` sets the id then calls `repo.Save` (`port/service.go:190-212`). The live matrix proves a stale Save succeeds, overwrites `Name`, and leaves the newer version intact (`test/integration/dialect_edge_test.go:380-418`); no stale Replace journey was found.
**Blast radius:** silent wrong answer

### E-SQLREPO-15 — MySQL collides on a second unique key during a primary-key Save
**Shape:** seam | adversarial input
**Setup:** A table has primary key `id` and a separate unique `email`. A caller saves `id=1` with an email already owned by `id=2`.
**What the consumer does:** They expect the non-primary unique collision to be refused, as it is on PostgreSQL, rather than have a primary-key Save update whichever row MySQL selected by `email`.
**What must happen:** The cross-dialect difference must be refused or made explicit at the call site; a consumer cannot safely treat `Save(id=1)` as targeting only id 1 when MySQL permits another unique key to choose the conflict row.
**Today:** ❌ wrong or unhandled
**Evidence:** `Save` always asks the dialect for one upsert tail keyed from the model primary key (`crud/sqlrepo/repository.go:609-624`, `:52-60`). PostgreSQL renders `ON CONFLICT (pk)` and declares that only the primary key is swallowed (`crud/dialect.go:75-98`); MySQL renders targetless `ON DUPLICATE KEY UPDATE` (`:126-154`), and the `UpsertScope` contract states that it swallows every unique key (`crud/dialect.go:36-47`). No integration case exercises a Save that collides only on a second unique key.
**Blast radius:** data loss

## Edge verdict

The worst new holes are declaration-time: two table scopes, or two relation
scopes on the same path, replace rather than compose, so a configuration made of
individually safe pieces can return rows it was meant to hide. Boundary and
configuration failures are also uneven: nil `Setting` and source values panic,
a negative maximum removes a cap, and a maximum finite page can overflow into an
unlimited read. Versioned `Update` is protected, but full-model `Save` and
`Replace` bypass that protection; on MySQL a second unique key can additionally
select a different row for the upsert. The normal first-use relation race and
middleware order are properly closed with source-level tests. Timeout ambiguity
is only a post-write commit-state concern; malformed foreign `RETURNING`
cardinality is an unverified executor contract, not a supported-adapter blocker.

## Release blockers found here (edge)
| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A second `sqlrepo.Scope` replaces the first (`crud/sqlrepo/blueprint.go:68-85`) | blocker | Two independently configured permanent guards can leave one guard absent from every read. This is a direct row-leak path and contradicts [[D-004]]'s rule that narrowing only composes. |
| 2 | A second `RelationScope` for one path replaces the first (`crud/sqlrepo/blueprint.go:228-236`; `crud/scope.go:43-52`) | blocker | A tenant or visibility predicate on a preloaded or relation-filtered table can be erased by another declaration for the same path. The far-side query then returns rows the first declaration existed to hide. |
| 3 | A stale versioned full-model `Save` (and therefore `Replace`) succeeds and overwrites newer fields (`crud/sqlrepo/repository.go:609-624`; `port/service.go:190-212`) | blocker | The `version` tag appears to protect concurrent writes, but one common full-replacement route silently wins instead of returning `ErrStaleVersion`. |
| 4 | MySQL `Save` can absorb a collision on a non-primary unique key and update that conflicting row (`crud/dialect.go:36-47`, `:126-154`) | blocker | A call targeting id 1 can mutate the row identified by another unique key, unlike PostgreSQL's primary-key-only conflict target. |
| 5 | `SkipTotal` overflows at `math.MaxInt` and emits no limit (`crud/sqlrepo/repository.go:171-177`; `crud/render.go:104-110`) | serious | A finite request can become a whole-table read and exhaust service memory. |

## Edge DX constraints

`Scope` and same-path `RelationScope` must have one declaration rule: AND-compose
every repeated narrowing or refuse the duplicate; table scope must not be
last-wins while relation scope is map-overwrite. The illustrative
`BatchSize(500)` is withdrawn: no arbitrary chunking knob should silently turn a
one-statement `SaveAll` into a partial multi-statement write. The smaller contract
is a declared per-statement ceiling and a pre-SQL refusal naming it; callers who
need more explicitly partition their input and own the transaction.

Any alternative conflict target or version-aware full Save is a direct [[D-011]]
challenge: `Save` is a single no-option JPA-shaped upsert, so it cannot be added
as a local convenience. A tombstone-view/Restore proposal also challenges
[[D-031]]'s statement-owned soft delete and needs [[D-030]]'s new-verb permission
and decorator obligations. These decisions must be amended explicitly before a
DX proposal becomes an API. The current source confirms `Scope` assignment
(`crud/sqlrepo/blueprint.go:68-85`), same-path overwrite
(`crud/scope.go:43-52`), and the existing one-statement `SaveAll` assembly
(`crud/sqlrepo/repository.go:1124-1175`).
