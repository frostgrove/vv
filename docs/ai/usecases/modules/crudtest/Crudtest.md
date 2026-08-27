# crud/crudtest — prove the statement your repository builds is the one you meant, on a laptop with no Docker

**Covers:** `github.com/frostgrove/vv/crud/crudtest`
**Sweep:** happy paths · edge cases · release readiness

**Verdict:** not ready — the code works and forty-one test files in this
repository lean on it, but two things get an order of magnitude more expensive
at the tag and are nearly free before it: whether an unanswered read is silent
(blocker 1) and whether a rendered statement is a compatibility promise (blocker
2). The edge half adds a false-green path in `Normalize`, which rewrites SQL
literals rather than formatting alone, plus invalid recorder construction and
shallow fixture/history capture that can make a test inspect data it never ran
with. **This sweep does not recommend tagging until rows 1 and 2 and the
normalization contract have an answer** — not necessarily the answer proposed
here, but an answer written down.

## What a consumer is actually trying to do

A team has forty container-backed tests and a CI run that takes fifteen minutes,
and somebody has been told to fix that. The question in front of them is not
"can I test without a database" — they can see that they can. It is **which of
their forty tests can move, which must not, and what the moved ones cost to
own.** That decision is the whole business model of this module, and nothing in
the tree currently helps them make it.

The answer that holds up is a line about what a test is *about*. A test about
what the library builds — the column that went missing from the `SET` list, the
argument in the wrong slot, the tenant filter that reached the `SELECT` and not
the `DELETE`, the page size off by one, the loop that quietly became twenty
thousand round trips — moves, and should. A test about what an engine does with
what was built — a unique index firing, a collation, two transactions taking
turns, whether the statement is even valid — cannot move and must not be made to
look as though it did. The first kind is most of a repository suite and runs in
two hundred milliseconds. The second kind is the reason the containers stay.

Then comes the part nobody warns them about, and it is the standing cost of
adopting this at all. A test that pins a statement is coupled to two things: the
library's rendering, and their own model. The second changes ten times as often.
Add one column to `User` and every pinned `SELECT` string in the suite goes red
at once — not wrongly, but all at the same time, by hand. Reorder two adjacent
fields of the same type and nothing goes red, every value lands in the wrong
field, and the suite is now green about the wrong thing. Whether that is a good
trade is a real question and the answer depends on how the assertions are
written, which is the one thing a team decides on day one and cannot revisit
cheaply.

The first thing everybody gets wrong is the same thing. **This is not a store.**
Rows fed in have nothing to do with statements recorded: save a model and read
it back in the same test, and the read answers whatever was queued next, which
may be the row the *previous* call was meant to get. Everyone arrives having
written a map-backed fake repository at some point and expects a round trip. It
does not happen, and the test that assumes it does goes green over a model the
double never saw.

The second thing they need is where the seam sits. Binding to a recorder
replaces the adapter and nothing above it. The repository, the gate, the specs
decorator, the fault enrichment and the wire compiler all run for real. The
driver does not, nor does the classification of a driver's error, nor a probe
against a live catalog. A test here proves the statement, not what any engine
does with it.

And there is one fear they have without knowing to name it: a test that goes
green because the double answered "nothing" to a question the test never meant
to ask.

## Happy cases

### H-CRUDTEST-01 — Pin the statement for a fetch by id, and the row that is not there
**Who:** the author on their first afternoon with the library, who does not yet trust the SQL it writes
**Wants:** the statement, the bound arguments, and the 404 path, in three lines of setup each
**Story:** They declare the repository, bind it to a recorder instead of a pool, queue one row, and call fetch-by-id. They compare the statement and the arguments against what they expected. Then they write the second test everybody writes — the id that does not exist becomes not-found becomes a 404.
**Must hold:**
1. Binding to the double is the same call as binding to a database — the declaration does not change.
2. The statement is readable after the call, and so are its arguments, in order and with their types.
3. The comparison does not pin the builder's whitespace.
4. Asking a recorder that has run nothing gives back an empty answer, not a panic.
5. *(Owned by H-CRUDTEST-02.)* "The read ran and matched nothing" is distinguishable from "the read never happened".
**Today:** 🟡 partial — (1) to (4) hold; (5) does not, and H-CRUDTEST-02 carries it.
**Evidence:** a source with a dialect and nothing else, so `Bind` takes it unchanged (`crud/crudtest/recorder.go:52-70`). `Last` answers the zero `Statement` on a fresh recorder (`recorder.go:104-110`), pinned by `TestLastAndSQLOnAFreshRecorder` (`crud/crudtest/recorder_test.go:71`). `Normalize` (`recorder.go:248`) with `TestNormalize` (`recorder_test.go:438`). The shape to copy is `TestGetByID` (`crud/sqlrepo/repository_test.go:69`); for the arguments it is `TestUpdateChecksTheVersionItReadAndAdvancesIt` (`crud/sqlrepo/version_test.go:34`) and `TestUpdateAllIsOneStatementForTheWholeFilter` (`crud/sqlrepo/updateall_test.go:15`), which read `.Args` by index. The 404 idiom is `TestGetByIDNotFound` (`repository_test.go:90`) — and it would pass identically with its `Push(crudtest.Rows())` deleted, because an unqueued read is also an empty result set (`recorder.go:147-159`).
**If not ready:** the consumer adds `if len(rec.Statements()) != 1` beside the error assertion, which is what the library does elsewhere and nothing tells a consumer to do here.

### H-CRUDTEST-02 — A paginated list, and the total that feeds the widget
**Who:** the author of the first list endpoint, checking the offset arithmetic
**Wants:** both statements, in order, and a page object built from a count they chose
**Story:** They queue two rows and a count of 57, ask for page 2 with a limit of 2, and assert the `LIMIT`/`OFFSET` on the first statement and the `count(*)` on the second. Then they check that the pager says page 2 of 29.
**Must hold:**
1. Both statements are recorded in order with their arguments.
2. The pager arithmetic is checkable from the count the test supplied.
3. How many answers to queue, and in which order, is knowable without reading the repository.
4. Queueing one too many, or one too few, does not pass quietly.
**Today:** 🟡 partial — (1) and (2) hold; (3) is written down in the wrong place; (4) does not hold at all.
**Evidence:** `TestGetPaginated` (`crud/sqlrepo/repository_test.go:108`) queues the rows first and the count second. That order, and the fact that the count is sometimes skipped, *are* documented — `docs/ai/flows/FL-001-list-request-to-rows.md:95-102` states all four COUNT cases, including "offset 0 and a short page … no COUNT is issued". They are not in `docs/modules/en/crudtest.md`, which is where someone writing a queue is looking, and whose only guidance is "Queue as many results as the call will make" (`:47-48`) — a sentence that is wrong for any statement the repository issues as an `Exec`, because `Exec` consumes nothing from the queue (`recorder.go:135-145` against `:147-159`). Guessing wrong is silent both ways: `TestGetSkipsCountOnShortFirstPage` (`repository_test.go:134`) shows the plan changing with the data, and the count row queued for a skipped count then sits at the head of the queue and answers the *next* read in the test.
**This case owns the positional queue.** Every other case that trips over it cites this one rather than restating it.
**If not ready:** the consumer guards every call with `len(rec.Statements())`, which is what the library's own tests do. Closing it is a strict mode plus one assertion that the queue was drained — and, before either, two corrected sentences in the module doc.

### H-CRUDTEST-03 — A partial update writes only the columns that changed
**Who:** the author of a `PATCH` endpoint, on the day someone reports a wiped field
**Wants:** to see the `SET` list, and to see that an unchanged field is absent from it
**Story:** They queue the row the repository will load, call update with one changed field and one unchanged one, and read the `UPDATE` off the recorder. The unchanged column is not in it.
**Must hold:**
1. The load and the write are both recorded, in order.
2. The `SET` list carries only what actually changed.
3. An explicit null binds a nil argument, and it is visible as one.
4. The statements a decorator adds in front are visible in the same recording.
**Today:** ✅ ready
**Evidence:** `Example_partialUpdate` (`_examples/example/example_test.go:27`) does exactly this, gate included, and prints all three statements off one recorder; `Example_nullVersusAbsent` (`example_test.go:55`) reads the nil argument straight out of `rec.Last().Args`. Three queued result sets, each labelled in a comment — the ceremony is H-CRUDTEST-02's.
**If not ready:** n/a.

### H-CRUDTEST-04 — The row the double hands back, and the day the model changed
**Who:** the author who has just added a column, and the author who has just moved one
**Wants:** to write a row literal without counting columns, and to be told when a literal stops matching the model rather than being filled into the wrong fields
**Story:** They write a row literal per fixture. Months later somebody adds `Nickname` to `User`, and every literal in the suite is one short. Somebody else swaps the declaration order of `Name` and `Email` — both strings — and nothing complains at all.
**Must hold:**
1. A queued row is assigned to the model the way a driver would assign it, converting where Go converts.
2. A row that cannot fill the destinations is an error naming what went wrong, not a silently zeroed model.
3. A null reaches a pointer as nil and a three-state optional as explicitly null, not as undefined.
4. A row that no longer matches the model is a loud failure, whatever kind of mismatch it is.
**Today:** 🟡 partial — (1), (2) and (3) hold and are well pinned; (4) holds for arity and **fails for order**.
**Evidence:** `TestScanFillsAModelThroughTheSchema` (`recorder_test.go:208`), `TestScanConvertsWhatGoWouldConvert` (`:262`) and `TestScanRefusesRowsItCannotFill` (`:294`) are the three-way proof of (1) and (2); `TestNullableColumnScansToNull` (`crud/sqlrepo/repository_test.go:97`) is (3), and [[UC-003]] is why it matters. The arity refusal is loud and early (`recorder.go:197-200`). Order is not checked and cannot be: `setValue` assigns anything assignable or convertible (`recorder.go:234-243`), so swapping two same-typed adjacent fields moves every value one field over, arity still matches, and the whole suite stays green. Adding a column is the *loud* case; reordering is the silent one, and it is the argument for a named row builder that the round-one draft made about the wrong half.
**If not ready:** nothing at the double is missing for (1) to (3). For (4) the consumer's defence today is a per-package `userRow(...)` builder, which is what three suites in this tree wrote (`crud/sqlrepo/repository_test.go:47`, `crud/decorators/specs/specs_test.go:47`, `_examples/example/example_test.go:19`) — and a builder with positional parameters moves the problem one level up rather than closing it.

### H-CRUDTEST-05 — An insert that comes back carrying its key
**Who:** the author writing the second test of their life against this library
**Wants:** the `INSERT`, the generated id on the model afterwards, and the rows-affected count
**Story:** They set the write result to say the database assigned 77, call save, and assert both the statement and that the model now holds 77. Then they run the same test under the other dialect their CI uses.
**Must hold:**
1. A write's rows-affected and generated key are settable, and the repository's handling of both is visible.
2. What a verb costs in queued answers is the same question under every dialect, or the difference is stated.
3. Setting the write result for one statement does not silently set it for the next one.
4. The dialect the test names is the dialect production runs.
**Today:** 🟡 partial — (1) holds; (2) is a real divergence documented in a flow, not here; (3) is false; (4) is false for MySQL and nothing says so.
**Evidence:** `TestSaveOnMySQLUsesLastInsertID` (`crud/sqlrepo/repository_test.go:273`) is the whole shape: on MySQL a save is an `Exec` that consumes nothing, needs `ExecResult{LastInsertID: 77, HasLastInsertID: true}`, and is followed by an unconditional read-back that does consume one (`crud/sqlrepo/repository.go:636-666`). On PostgreSQL the same call is one `INSERT … RETURNING`, a `Query`, which consumes one and needs no `ExecResult`. The fork is `docs/ai/flows/FL-003-save-insert-versus-upsert.md:79-88`. For (3): `ExecResult` sets a field every later `Exec` returns (`recorder.go:81-86`, `:135-145`), while the module doc calls it "the next `Exec`'s" (`docs/modules/en/crudtest.md:53`) — sitting directly above `Fail`, which really is one-shot. For (4): `crudtest.MySQL()` is `crud.MySQL{}` (`recorder.go:68`), which is `RowAlias: false` — the MariaDB and MySQL 5.7 upsert spelling. An application that declares `crud.MySQL{RowAlias: true}` in production and writes `crudtest.MySQL()` in its test pins `ON DUPLICATE KEY UPDATE c = VALUES(c)` against a service that emits `AS new … new.c` (`crud/dialect.go:126-153`). The divergence is live in this repository — `test/integration/driver_sql_test.go:41` and `test/integration/dialect_edge_test.go:163` both run `RowAlias: true` — and no recorder-backed test anywhere uses it.
**If not ready:** the consumer discovers the plan by running the call once and reading `rec.Statements()`, or by reading FL-003. `Statement.Query` (`recorder.go:22`) already says which shape each recorded call was, and is named in no consumer-facing document. For (4) the fix is one sentence next to the shorthands.

### H-CRUDTEST-06 — Two people edit the same row
**Who:** the author of an admin screen where two operators have the same record open
**Wants:** proof that the second write is refused as stale, and that a deleted row is a different answer
**Story:** They queue the load, then an `UPDATE` that matched nothing, then a probe read that finds the row still there. The call comes back stale, and the handler turns it into a 409. They write the sibling test where the probe finds nothing, and it comes back not-found.
**Must hold:**
1. The three-statement plan is queueable, and the two endings are distinguishable by what the test queued.
2. The version predicate and the version increment are both visible in the statement.
3. The dialect without `RETURNING` reaches the same two answers, and the test can drive it.
**Today:** ✅ ready — and it is the best-argued sequence of recorder tests in the tree.
**Evidence:** `crud/sqlrepo/version_test.go:34` pins both halves of the lock in one statement and reads `Args[2]` to prove the update was pinned to the version that was read. `TestAnUpdateAgainstARowSomebodyElseChangedIsRefused` (`:58`) drives both dialects; `TestAVanishedRowIsStillNotFoundRatherThanStale` (`:100`) is its control, differing only in the third queued result. The MySQL arm is also the cleanest illustration of H-CRUDTEST-05 (3): `rec.ExecResult(crud.Result{RowsAffected: 0})` at `:84` is correct there because the arm issues one `Exec` — add a second write to that test and the zero sticks to it too.
**If not ready:** n/a.

### H-CRUDTEST-07 — Delete is an UPDATE
**Who:** the author who declared soft delete on a table and now has to prove it everywhere
**Wants:** the tombstone stamp instead of a `DELETE`, and "not deleted" in every read that could show one
**Story:** They call delete against a recorder and assert an `UPDATE … SET deleted_at`. Then they run a list with a preload and check the tombstone filter reached the second statement as well as the first.
**Must hold:**
1. The stamp is an `UPDATE`, is one statement, and carries exactly the narrowing the `DELETE` would have had.
2. A control — the same declaration without soft delete — shows the difference is the setting and not the fixture.
3. The read filter reaches the preload's own statement, which is a different table.
4. The stamped timestamp is assertable.
**Today:** 🟡 partial — the narrowing half of (1) and all of (2) are proven once, in another package's test; the rest of (1) is unproven anywhere; (3) has no unit test; (4) needs a package variable swapped and nothing says so.
**Evidence:** the only unit-level proof in the tree is a subtest of the security suite: two repositories over the same table under the same policy, differing only in `SoftDelete`, with the hard one as the control (`crud/decorators/security/relscope_test.go:398-421`). **Read it before trusting it** — its whole assertion body is two `strings.Contains` checks for `rx1."tenant_id"` on `hard.Last().SQL` and `soft.Last().SQL`. It never asserts the soft path produced an `UPDATE`, never asserts `deleted_at` appears, and never asserts a statement count, so it would pass unchanged if the soft repository emitted a hard `DELETE` carrying the same `WHERE`. Everything else about soft delete is integration-only: `test/integration/softdelete_test.go` has six tests and `crud/sqlrepo` has none. The preload half is the one people forget, and the code says why: "a preload is a second statement against a second table, so the parent query's WHERE does nothing for it" (`crud/preload.go:263-266`). For (4), the stamp binds `crud.NowFunc()` (`crud/sqlrepo/repository.go:936`) — a package-level `var` (`crud/meta.go:15-17`) whose own doc comment tells you to swap it in a test. That is the only clock this library touches; see H-CRUDTEST-19 for what swapping it costs under `-race`.
**If not ready:** nothing is missing from the double. What is missing is that a consumer with soft delete has no worked example, will not think to check the preload, and — if they copy the one test that exists — will copy an assertion that does not pin the thing its own comment says it pins. One unit test in `crud/sqlrepo`, four lines in the module doc, and a strengthened assertion in the existing subtest. [[D-031]] declares both halves of soft delete and its **Proven by** section is integration-only, so it wants the new test too.

### H-CRUDTEST-08 — The tenant filter is in every statement, and a refusal issues none
**Who:** the author of a B2B product wiring up row-level scope
**Wants:** proof that the narrowing reached the `UPDATE` and the `DELETE`, not only the `SELECT` — and that a refused write never became SQL
**Story:** They bind the repository through the gate to a recorder, put a principal in the context, call every verb, and read the `WHERE` clauses back. Then they call one the policy forbids and assert the recording is empty.
**Must hold:**
1. Every verb's statement is recorded, so no verb can be forgotten.
2. A refusal that happens before SQL records nothing, and that is readable in one line.
3. It stays empty for a refusal from any layer — a validator, a gate, a mapper.
4. A control — the same call with no policy — shows the narrowing genuinely absent.
**Today:** 🟡 partial — (1), (2) and (4) are as well proven as anything in this repository; (3) is claimed only by uncompiled markdown.
**Evidence:** the entire gate suite is built on the recorder — `crud/decorators/security/security_test.go`, `relscope_test.go`, `gate_edge_test.go`, `edge_test.go`, `obligation_test.go`. The zero-statement half is asserted verb by verb inside `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason` (`obligation_test.go:143`) over a table that includes `Count`, `Exists` and `Aggregate` (`:45-56`), and printed as output by `Example_frozenColumn` (`_examples/example/example_test.go:114`). The control pattern is `TestAPreloadIsNotNarrowedWithoutTheDeclaration` (`relscope_test.go:102`), whose failure message says outright that the positive test above it proves nothing if it passes. **(3) is the weak one.** The only evidence for a *validator* or *mapper* refusal recording nothing is two fenced code blocks in the usage guides (`docs/usage-guides/gorm.md:1127-1139`, `docs/usage-guides/ent.md:1206-1218`), and `TestServiceRejectsEmptyName` and `TestServiceRejectsBadEmail` exist in neither the tree nor `_examples/`. Nothing compiles them. By [[D-020]] that is not evidence.
**If not ready:** the guarantee holds structurally — a refusal above the repository never reaches `Source` — but a consumer's first service-layer test is the one the guides show them, and it is the one nothing runs. Moving those two snippets into `_examples/` makes them compile under `make examples` and costs about twenty lines.

### H-CRUDTEST-09 — "Orders where the customer's country is DE"
**Who:** the author of a list endpoint whose filter crosses a relation
**Wants:** to see what a filter through a relation renders as, and to know it did not quietly duplicate rows or escape the tenant scope
**Story:** They filter on a field of the related table, run the read against a recorder, and read the statement back. They check it is one statement, that it did not become a join, and that the tenant narrowing is still in it — and in the subquery too.
**Must hold:**
1. A filter through a relation is one statement, and a row cannot come back twice because of it.
2. The narrowing on the far table is visible in the same recorded statement.
3. Two hops read as two nested subqueries, not as two joins.
4. The tenant scope reaches inside the subquery, not only around it.
**Today:** ✅ ready at the predicate level, 🟡 through a bound repository — the property holds and is well pinned; what is missing is that nothing tells a consumer this is a *different* case from a preload.
**Evidence:** a relation path renders as a correlated `EXISTS`, and the code names the reason: it "keeps `Comments.Body eq x` from multiplying" the result (`crud/predicate.go:84`, rendering at `:109-122`). The table of shapes is `crud/predicate_test.go:159-209` — `belongs_to` correlating on the local FK, `has_many` on the remote one, two hops nesting one `EXISTS` inside the other, and `NOT EXISTS` for "has no matching child". (4) is pinned through a bound repository by `TestCountAndExistsNarrowTheirSubqueries` (`crud/decorators/security/relscope_test.go:198-222`), which counts two `"tenant_id" = $` per statement — the near table and the far one.
**If not ready:** nothing is missing. What is missing is the sentence a consumer needs: **a relation in a `filter` is one statement and a relation in `preload` is two** (H-CRUDTEST-15), and the queue arithmetic is completely different. Somebody who conflates them queues two answers for a relation filter and silently poisons the next read, which is H-CRUDTEST-02's failure with a new entry point.

### H-CRUDTEST-10 — An untrusted query document compiles to the `WHERE` I expected
**Who:** the author exposing a filter to a front end
**Wants:** to see what a JSON filter turned into, as SQL, without a database
**Story:** They compile the document, bind a repository to a recorder, run the read, and cut the `WHERE` clause out of the recorded statement so the `SELECT` list does not drown the assertion.
**Must hold:**
1. The clause can be compared without the surrounding `SELECT` list and pagination tail getting in the way.
2. A filter written as JSON and the same filter written as `crud.Where(...)` produce the same clause, so a consumer can migrate between the two spellings without re-deriving expectations.
**Today:** 🟡 partial — both work, and every suite writes the extractor itself, four times, in two behaviours that do not agree.
**Evidence:** four named helpers cut a `WHERE` out of statement text and they are not one helper. Two trim the tail: `where` (`crud/query/query_test.go:110-116`) and `where` (`_examples/example/blog/blog_test.go:30-36`) both loop over `" ORDER BY "`, `" LIMIT "`, `" OFFSET "`; `lastWhere` (`crud/decorators/security/security_test.go:55-61`) does the same and takes the recorder rather than a string. Two do not trim: `whereOf` (`crud/decorators/security/relscope_test.go:464-467`) is a bare `strings.Cut`, and `where` (`crud/decorators/specs/specs_test.go:51-60`) builds a recorder, runs a `Specification` through it, then bare-`Cut`s and returns the args too. The bare pattern also appears inline three more times (`crud/preload_edge_test.go:104`, `:126`, `_examples/example/blog/blog_test.go:120`, the last cutting at `SET`). For (2), `crud/query/roundtrip_test.go:18` renders a predicate for exactly this comparison and it is the shape a consumer would copy.
**Note:** determinism is what makes any of this assertable and it is [[D-014]], not this module's guarantee. It belongs to the `crud/query` sweep, and so does the cross-release half of the same question — see H-CRUDTEST-20, which states what *this* module's doc has to say under each answer. Refusal-produces-no-statement is H-CRUDTEST-08 (2).
**If not ready:** the consumer writes the extractor a fifth time and picks one of the two behaviours by accident. A shipped `crudtest.WhereOf` has to pin which — see the DX section, where it does, and names the two call sites whose assertions change on adoption.

### H-CRUDTEST-11 — Infinite scroll: the cursor, the second page, and "is there more"
**Who:** the author of a feed endpoint that pages by cursor rather than by offset
**Wants:** to assert the second page's `WHERE`, and that "there is a next page" is right
**Story:** They queue a page of rows, ask for a limit with the total skipped, and assert `HasNext` and the emitted cursor. Then they feed that cursor back, run again, and assert the tuple comparison that carries the walk forward.
**Must hold:**
1. The `SELECT` a cursor page issues is recorded and its comparison is readable.
2. "There is a next page" is derivable from what the test queued, with a stated rule for how many rows that is.
3. A cursor comes back on a page whose sort supports one, and its absence has a reason a test can see.
4. Feeding the cursor back produces the tuple comparison, and the arguments are the last row the double returned.
**Today:** ❌ missing at unit level — the double can express all four and **nothing in the tree does**. Every cursor test is integration-only.
**Evidence:** `grep` for `crud.After(` / `crud.Before(` / `NextCursor` across `*_test.go` returns `test/integration/cursor_test.go` and nothing else. Three silent-green traps sit here and none is written down anywhere. **First:** with `SkipTotal` and a limit the read fetches `limit+1` rows and drops the extra (`crud/sqlrepo/repository.go:171-192`), so a consumer who queues exactly `limit` rows to test "there is more" gets `HasNext: false` and a green test asserting the opposite of what they meant. The library's own `TestSkipTotalProbesOneExtraRow` (`repository_test.go:148`) queues three for a limit of two and is the only place that rule is visible — as a fixture, not as a sentence. **Second:** cursors are emitted only when the sort is unique; `setCursors` returns early otherwise (`repository.go:245-253`), so a fixture sorted on a non-unique column hands back an empty `NextCursor` with no explanation, and the test author concludes the feature is broken. **Third:** the second page's `WHERE` is a tuple comparison built from the sort that actually ran, including the tiebreaker (`repository.go:337-352`) — the single statement most worth pinning, and the one nothing demonstrates against a recorder.
**If not ready:** everything needed is present; a consumer has to derive the `limit+1` rule and the uniqueness rule from the source. Two paragraphs in the module doc and one worked example close it, and the example is cheap because the machinery is already there.

### H-CRUDTEST-12 — The dashboard read, where the result set is not the model
**Who:** the author of a reporting endpoint — a badge count, an existence check, `SUM(total) GROUP BY status`
**Wants:** to queue an answer for a read whose columns are not the model's, without guessing the arity
**Story:** They call count, then exists, then an aggregate with one grouping and two sums, and then a read narrowed with a select list. Each one needs a queued answer of a different width.
**Must hold:**
1. Each of these verbs is one statement and it is recorded.
2. The arity of the answer each one wants is stated somewhere a consumer reads.
3. The narrowing every other read gets is on these statements too.
**Today:** 🟡 partial — (1) and (3) hold and are pinned; (2) is stated nowhere and has four different answers.
**Evidence:** the four arities, none of them the model's. `Count` scans into one destination (`crud/sqlrepo/repository.go:1003-1017`), so it wants `Rows([]any{int64(0)})`. `Exists` never scans at all — it asks `rows.Next()` and stops (`:587-604`) — so `Rows()` answers false and a row of *any* width answers true. `Aggregate` scans `len(GroupBy) + len(Aggregations)` cells (`:1083-1095`). A read narrowed by `crud.Select` scans the selected columns only. The recorder refuses a wrong arity loudly (`recorder.go:197-200`), which is the right behaviour and no help in choosing. (3) is proven: `TestCountAndExistsNarrowTheirSubqueries` (`crud/decorators/security/relscope_test.go:198-222`) is the only place in the tree that queues for a `Count` and an `Exists` and it does so with two different shapes, without a word about why. **Nothing anywhere queues rows for an `Aggregate`** — the two tests that call it (`crud/decorators/security/obligation_test.go:54`, `crud/decorators/faults/faults_test.go:296`) are a gate refusal and a failure injection, so the happy path has no example in this repository at all.
**If not ready:** the consumer runs the call once against a recorder, reads the statement, and counts the columns by hand. This is also the case where the DX section's `RowsOf[M]` proposal cannot help by construction, which is why it needs the companion it now has.

### H-CRUDTEST-13 — The write that fails, the read that fails, and the read that fails halfway
**Who:** the author who just added a unique index on `email`, and the author whose large read got killed by the server
**Wants:** one statement to fail, and to see what the caller ends up holding
**Story:** They arm the recorder with the driver's error, call save, and assert the error the repository returned. Separately they drive the arm where a read yields three rows and *then* fails, and check the caller is not handed three rows as a complete answer.
**Must hold:**
1. Exactly one statement fails; the next one succeeds, so a sequence can be tested.
2. It fails whichever way the repository issues it — some verbs are a write on one dialect and a read on another.
3. A read that yields rows and then fails is expressible, and a partial answer is not reported as a whole one.
4. The failure can be a driver-shaped error that the library's own classification runs on.
5. A failed statement is still in the recording.
**Today:** 🟡 partial — (1), (3) and (5) hold; (2) is manual and costs (1); (4) is reachable, but not through a recorder-backed repository.
**Evidence:** `Fail` clears itself after one `Exec` (`recorder.go:88-95`, `:135-145`), pinned by `TestExecResultAndOneShotFailure` (`recorder_test.go:179`). **(3) is `RowsFailing`, and it is the best-argued thing in this package and the least used.** `Result` carries `Err` and `RowsErr` as two fields because the drivers disagree about where a failure surfaces: `Err` is `database/sql`'s refusal at `Query`, `RowsErr` is pgx's — a refused statement arrives as a live `Rows` that yields what it has and then answers `Err`. The doc comment names the bug class it exists for: "a loop that never asks reads a truncated schema as a complete one" (`recorder.go:29-35`, restated at `docs/modules/en/crudtest.md:58-65`). It is pinned by `TestARowsErrorArrivesAfterTheRowsRatherThanInsteadOfThem` (`recorder_test.go:136`) and used in anger exactly once, in the catalog's own suite (`crud/catalog/load_test.go:73`), which [[D-041]] names under **Proven by**. For (2): a failed read is a different call — `Push(crudtest.Result{Err: err})` — and nothing at the double tells a test which shape a verb will use, so the library's own suite arms both and pushes four spare copies (`crud/decorators/faults/faults_test.go:57-67`, whose comment names the reason: a save is an `Exec` on MySQL and a `Query` on PostgreSQL). Those four spares are what costs (1): the next four reads fail too. `.Fail(` has exactly two call sites in the whole repository.
For (4), classification is reachable in memory. `sqlfault.New(engine)` and `sqlfault.Wrap(c, err)` are exported and pure (`crud/sqlfault/classify.go:44`, `:137`), `Extract` reads a driver error by shape rather than by type (`crud/sqlfault/extract.go:15-46`), and the package's fixtures are hand-written pgconn/mysql/sqlite shapes with no driver imported (`crud/sqlfault/fixture_test.go:12-60`). Three HTTP bindings already assert the whole hop in a unit test (`crud/http/crudnet/write_edge_test.go:242` and its twins). What is missing is narrow: a repository bound to a *recorder* never classifies, because only `crudsql` and `crudpgx` hold a classifier (`crud/adapter/crudsql/conflict.go:16`).
**If not ready:** for (2), a `FailNext` that catches whichever call arrives — and see the DX section, which argues `Fail` should have been that from the start. For (3), the double is complete and the module doc explains it well; what is absent is any hint about *which* of the two a consumer's own driver will produce, which is a two-line table. For (4), the consumer either injects an already-classified fault (`faults_test.go:46-51` is the recipe) or writes a ten-line `crud.Source` forwarding to the recorder through `sqlfault.Wrap`. The second is the one nothing demonstrates and the one worth documenting.

### H-CRUDTEST-14 — A service wraps two writes in one transaction
**Who:** the author of an order service that must not half-commit
**Wants:** to assert one transaction was opened, both statements ran inside it, and it committed — and that the failing path rolled back
**Story:** They run the service against a recorder, once succeeding and once with the second write failing, and assert the two endings differ.
**Must hold:**
1. One transaction is counted, and a call that finds an ambient one joins rather than opening a second.
2. The statements inside it are in the same recording, in order.
3. A commit is observable.
4. A rollback after an error is observable.
**Today:** 🟡 partial — (1) and (2) hold and are properly pinned; (3) and (4) do not hold at all.
**Evidence:** (1) is covered twice over a recorder, and the round-one draft was wrong to call it unproven. `TestTransactionJoinsAnAmbientExecutor` (`crud/sqlrepo/repository_test.go:561-580`) binds to one recorder, puts a second in the context with `crud.WithExecutor`, and asserts `rec.TxDepth() != 0` fails plus `len(outer.Statements()) != 1 || len(rec.Statements()) != 0`. `TestTheRecorderStaysUnidentified` (`crud/crudtest/recorder_test.go:457-500`) drives `crud.InTx` over a recorder, asserts a repository bound to a *different* recorder joined it, and carries `identifiedRecorder` (`:503-509`) as the control that fails if the two cases ever stop differing — the shape [[D-020]] asks for. (2) is `TestBeginRecordsIntoTheSameRecorder` (`recorder_test.go:372`) and `TestRecorderDrivesInTx` (`:400`). **(3) and (4) are unreachable.** `Commit` and `Rollback` set fields on an unexported `tx` (`recorder.go:169-176`) and nothing anywhere reads either; the tree's own proxy is `TestTransactionRollsBackOnError` (`repository_test.go:582-597`), which asserts the error came back and `TxDepth() == 1` and would pass identically for a transaction left open. That is a shipped guarantee the code cannot satisfy: [[UC-011]] guarantee 11 promises "a test asserts the begin, the statements and the commit in one place". Separately `TxDepth` counts begins and never decrements (`recorder.go:128-133`, `:162-167`), so two *sequential* transactions read as depth 2 — which matches UC-011's own wording ("the number of transactions opened is readable") and contradicts the module doc's "how deep the transaction nesting went" (`docs/modules/en/crudtest.md:74`). The doc is the outlier; that turns a design question into a one-line correction with the contract on its side.
**If not ready:** the consumer asserts the error and that no statement follows it. Closing (3) and (4) is new bookkeeping, not a read of existing state: `Begin` returns a `&tx{Recorder: r}` that is never stored, and the `Recorder` struct holds statements, queue, exec, execErr and txDepth and nothing else (`recorder.go:52-61`). Recording `BEGIN`/`COMMIT` as statements would also close it and would change `len(rec.Statements())` for every transactional test in this repository and in every consumer's — which is the argument for an accessor instead.

### H-CRUDTEST-15 — A preload is a second statement, and it must stay one
**Who:** the author who exposed `?preload=comments` and has been bitten by N+1 before
**Wants:** the batched second query with the parent keys bound, and a test that keeps the count at two for one row and for a hundred
**Story:** They queue the parents and then the children, run the read, assert two statements with the second carrying the ids from the first, and assert the *number* of statements so a regression is loud.
**Must hold:**
1. Both statements are recorded in order.
2. The second one's arguments are the keys the first one returned.
3. The statement count does not grow with the number of parent rows.
4. How many results to queue is derivable from what was asked for.
**Today:** 🟡 partial — (1), (2) and (3) hold; (4) is false in both directions.
**Evidence:** `crud/preload_test.go` and `crud/preload_edge_test.go` are built entirely this way. (4) is where a consumer counting preload terms goes wrong twice over. One nested path is one term and *two* statements: `specs("Owner.Pets")` needs two queued results (`crud/preload_edge_test.go:56-63`) because the path is split on `.` and walked segment by segment (`crud/preload.go:73-88`). Two terms sharing a prefix fold into *one* statement, which the code states outright — "Folding two requests for one relation into a single query is what lets 'Comments' and 'Comments.Author' share a statement" (`crud/preload_edge_test.go:75-78`). And the many-to-many second statement's row is one column *wider* than the target model: it selects the join table's owner key ahead of the target's columns (`crud/preload.go:272-289`), so a row literal built from the target model is refused for arity and the message says nothing about why. Compare H-CRUDTEST-09: a relation in a *filter* is one statement, not two.
**If not ready:** the count assertion is one line and works today; nothing in the docs suggests writing it, and it is the single most valuable regression test a unit-level double can hold.

### H-CRUDTEST-16 — The nightly import that must not become twenty thousand round trips
**Who:** the author of a bulk sync, on the day the batch window stopped fitting
**Wants:** proof that eight thousand rows are one statement, and that "rename every user in this tenant" is one `UPDATE` and not a loop
**Story:** They call the batched save with a slice, assert one recorded statement and the row count inside it, and then call the filtered update and assert a single `UPDATE … WHERE`. They also check that a batch mixing generated and supplied keys is refused rather than quietly split.
**Must hold:**
1. The statement count is the assertion, and it is one line.
2. A batch the library refuses to split is refused visibly, with a reason.
3. The write result feeds the count the caller gets back.
**Today:** ✅ ready for the filtered update; 🟡 partial overall — nothing unit-tests the batch size.
**Evidence:** `TestUpdateAllIsOneStatementForTheWholeFilter` (`crud/sqlrepo/updateall_test.go:15-33`) is the model: an `ExecResult` for the count, `len(rec.Statements()) != 1` for the shape, and the arguments read by index. The batching and its refusal are `crud/sqlrepo/repository.go:1124-1175`, and the refusal says why in the error text — "they are two different statements, and splitting the batch would hide the cost" (`:1146`). No test in `crud/sqlrepo` asserts the batched insert's shape against a recorder. That gap belongs to the `crud/sqlrepo` sweep; what is crudtest's here is only that the assertion is one line and nothing recommends it.
**If not ready:** the assertion is available and unwritten. This is the headline reason people adopt a repository library over hand-written SQL, and an integration test proves it worked, not that it was one statement.

### H-CRUDTEST-17 — The same declaration, checked under every dialect the app might ship on
**Who:** the author who develops on SQLite and deploys on PostgreSQL
**Wants:** one table-driven test that runs the same call under each dialect
**Story:** They loop over the dialects, giving each case its own recorder and its own queue, and assert the placeholder style and whether a `RETURNING` clause appears.
**Must hold:**
1. Each shipped dialect has a one-word constructor, and each shorthand's hidden defaults are stated.
2. State does not leak between cases.
3. Resetting really resets.
**Today:** 🟡 partial — (1) is two thirds there and the hidden default is unstated (H-CRUDTEST-05 (4)); (2) holds only because every test in the tree builds a fresh recorder; (3) is false.
**Evidence:** `Postgres()` and `MySQL()` exist and SQLite does not, so the third shipped dialect is spelled `New(crud.SQLite{})` — including in the library's own dialect test (`recorder_test.go:418-436`) and in the module doc's own example (`docs/modules/en/crudtest.md:34`). `Reset` clears the statements and the queue and leaves the exec result, the pending failure and the transaction count behind (`recorder.go:121-126`), while the module doc's table says "clear everything" (`docs/modules/en/crudtest.md:75`). Its own test only exercises the two that work (`TestResetClearsTheRecordingAndTheQueue`, `recorder_test.go:348-368`) — a control for the other three would fail today. A `Fail` armed in one subtest and not consumed there fails a statement in the next one, which is a red test in the wrong place; `Reset` has seven call sites outside crudtest's own suite. Reuse of one recorder across cases is therefore not a thing to want; the shape that works is a recorder per arm with its own queue, because the plans differ per dialect: `TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect` (`crud/sqlrepo/repository_test.go:438-460`) has a two-result Postgres arm and a MySQL arm with an `ExecResult` and two results.
**If not ready:** a fresh recorder per case, which nothing tells a consumer to do. `Reset` is a one-line fix and the doc sentence is a fifteen-second one.

### H-CRUDTEST-18 — Drive the handler over a repository that is not there
**Who:** the author who wants to assert that `?sort=-price&preload=owner` reached the repository as the options they meant
**Wants:** a stand-in repository that records what it was called with, so the assertion is about the request and not about SQL
**Story:** They mount the handler over a double, fire a request, and read back the method, the id, the DTO and the compiled options.
**Must hold:**
1. There is a repository double they can take, rather than write.
2. It records every method call, with what id, model, DTO and options — not only the last one, because the commonest assertion is over a load-then-write pair.
3. One method can be made to fail while the others succeed, because the commonest error test is a `PATCH` whose row vanished between the load and the write.
4. A `POST` through the default double returns a body whose id field is non-zero, and a `PATCH` returns a model with the DTO's fields applied — so a handler assertion over the response body cannot pass against an empty object.
5. The compiled options are inspectable as data — the filter as SQL, the sort terms, the preload paths.
**Today:** 🟡 partial — the seam is public and works; the double is not shipped.
**Evidence:** the handler takes an interface ([[D-022]]), so (1) is possible for anyone. It has been written four times inside this repository and exported zero times: `crud/http/crudnet/fake_test.go`, `crudgin/fake_test.go`, `crudfiber/fake_test.go`, `crud/rpc/crudgrpc/fake_test.go` (376, 392, 385 and 308 lines, of which the double proper is about 160 each). All four record a `calls` slice, so (2) is what they converged on and `Last()` alone would not be. None of the four answers (3): a single `err` field fails every method (`crud/http/crudnet/fake_test.go:96-97`, and the same comment in the other three). **Three of the four** answer (4) through two behaviour hooks — `onSave` assigns the key and fills the generated column, `onUpdate` applies the DTO (`crudnet/fake_test.go:100-141`) — which is the 33 lines a double answering zero values would leave every consumer to rewrite. The fourth, `crudgrpc`, hardcodes the same behaviour in the method bodies and carries neither hooks nor option inspectors, so it answers (5) not at all; `whereSQL`, `predSQL`, `preloadPaths`, `relMeta` and `sortTerms` exist in the three HTTP fakes only.
**A challenge to [[UC-011]], stated plainly.** UC-011 lists "A shipped fake repository for the handler" under **Out of scope**, and says guarantee 12 "describes what is possible through the public interface, not a facility the library hands over". Its Status then calls the same thing "the gap", and `docs/ai/usecases/Index.md:217` carries it as open tension 9. Proposing to ship the double widens that scope, and that is the owner's decision, not this sweep's. What is *not* a scope question: `mount` cannot ship from here, because it names the binding's own `Option` type and no binding is on `scripts/checks.sh:TIER0`. The `httptest` harness around it (`do`, `ok` — `crudnet/fake_test.go:266-290`) uses `net/http`, `net/http/httptest` and `encoding/json` only and is tier-legal; whether it *belongs* here is a design question, not a manifest one. The round-one draft got that backwards.
**If not ready:** the consumer writes the double a fifth time, or binds a real repository to a recorder, mounts the handler over that, and asserts the SQL a request produced. Nothing in the tree demonstrates the second road — no example under `_examples/` mounts a handler at all.

### H-CRUDTEST-19 — The suite runs `-race`, and half of it runs `t.Parallel()`
**Who:** the author whose shop defaults to `go test -race ./...` and marks every unit test parallel
**Wants:** to know whether any of this is safe, and if not, which part
**Story:** They adopt the idiom, mark their repository tests parallel because they are fast and independent, and one of them swaps the soft-delete clock. CI goes red intermittently, in someone else's package.
**Must hold:**
1. Whether one recorder may be shared across goroutines is stated, and so is what happens if it is.
2. Anything process-global a test is told to change is flagged as process-global at the point it is recommended.
**Today:** ❓ unverified in the docs, and the answers differ — which is exactly why they need writing down.
**Evidence:** the recorder itself is safe for concurrent use and deliberately so: every method takes one `sync.Mutex` (`recorder.go:55`) and `Statements()` returns a copy rather than the live slice (`recorder.go:97-101`), pinned by `TestStatementsIsACopy` (`recorder_test.go:334`). But **safe is not the same as useful**: the queue is positional, so two parallel subtests sharing one recorder do not race visibly, they interleave and answer each other's reads — H-CRUDTEST-02's failure with a scheduler attached, and therefore intermittent. `crud.NowFunc` is a plain package-level `var` (`crud/meta.go:15-17`) whose own doc comment says "Swap it in a test rather than…", and H-CRUDTEST-07 is where a consumer is told to swap it: two parallel tests doing that is a data race the detector will find, filed against this library. The schema cache is a `sync.Map` and is fine (`crud/meta.go:187-205`). [[D-020]] forbids `t.Parallel()` in this repository and gives its reason — shared physical tables in `test/integration`, plus a preference for a uniform rule — but that reasoning does not reach a consumer's own package and nothing says what does.
**If not ready:** three sentences in the module doc: the recorder is mutex-guarded, one recorder per test all the same because the queue is positional, and `crud.NowFunc` is process-global so a test that swaps it cannot be parallel. All three are true today and cost nothing.

### H-CRUDTEST-20 — Four hundred pinned strings meet a fifth column
**Who:** the team lead deciding whether the whole company writes tests this way
**Wants:** to know what this suite costs to own after the first year
**Story:** They adopt the style, accumulate four hundred asserted statement strings across twenty resources, and then somebody adds a column to the busiest model. Six months after that, somebody runs `go get -u`.
**Must hold:**
1. Adding a field to a model breaks the pinned assertions in a way that is mechanical rather than a rewrite, or the recommended assertion is narrower than the whole statement.
2. Either the rendered statement text is a compatibility promise across releases, or the documentation says it is not and recommends the narrower assertion.
**Today:** 🟡 for (1) — the failure is loud and the remedy is manual; ❓ for (2), which is not this module's decision to make.
**Evidence:** for (1) the added column widens the `SELECT` list, so every whole-statement assertion over that model fails at once, by hand, all four hundred. Nothing about the double changes that; what changes it is asserting a clause rather than a statement, which is H-CRUDTEST-10's extractor and the second reason to ship it. The *silent* half of the same problem — a reordered field, where nothing goes red and every value lands one field over — is H-CRUDTEST-04 (4). For (2), [[D-014]] guarantees byte-identical SQL for the same document and names four reasons — test assertions, plan caches, diffing, debugging — every one of them within a single build. It says nothing about two versions of the library, and no decision does. `git tag` is empty, so today the question has no wrong answer; at the first tag it acquires one.
**Note on placement.** (2) is a property of the builder, not of the double, so **this sweep asks the `crud/query` sweep to decide it** and does not decide it here. What this module owes is the consequence, and it is writable under either answer: if the answer is "the text is stable across minor versions", `docs/modules/en/crudtest.md` says so and whole-statement assertions are the recommended idiom; if it is "no promise", the same page says that and leads with the clause extractor instead. Either sentence is cheap. The absence of both is what makes a team lead guess.
**If not ready:** they guess, pick whole-statement assertions because that is what every example in this repository shows, and find out at the first upgrade.

### Out of this module's reach

**The catalog.** A statement whose shape depends on what the table actually looks
like — the fault probe's one-boolean-per-constraint read — is not a `crudtest`
case. [[D-048]] puts `crud/catalog` outside the contract manifest, so
`crud/crudtest` cannot import it and `make check-tiers` fails the moment it
tries. The pattern that works is a hand-built `catalog.Catalog` — three methods
over exported structs (`crud/catalog/catalog.go:13-19`) — handed straight to
`probe.Full` (`crud/probe/full.go:18`), which the probe's own suite already does
in about sixty lines of plain literals (`crud/probe/fixture_test.go:47-135`).
That belongs in the `catalog`/`probe` sweep. Round one also claimed a
catalog-derived upsert conflict target existed; it does not. `Dialect.Upsert`
takes the primary key *and the columns* — `func (d Postgres) Upsert(pk string,
cols []string) string` (`crud/dialect.go:80`, `:126`, `:182`) — and what comes
from the primary key alone is the conflict *target*, not the parameter list.
`crud/sqlrepo` never mentions a catalog.

**Read/write routing.** "The list went to the replica and the write did not" is
a `crud` guarantee ([[D-032]], `crud/executor.go:83-96`,
`crud/sqlrepo/repository.go:33-37`), not a crudtest capability. The double's
whole contribution is that there can be two of them, and two recorders behind a
`crud.ReadWrite` pair already appear in the tree for a different purpose
(`crud/catalog/set_test.go:203-206`). Nothing is missing here and no crudtest
change would close it; the missing unit-level demonstration is filed against
`crud` or `_examples`, and the live twin is `test/integration/replica_test.go:36`.
Round one carried this as a case and it produced no action.

**The module has no flow of its own, and that is where the doc fixes belong.**
`docs/ai/flows/Index.md:127` maps `crud/crudtest/recorder.go` to FL-016, which is
the *catalog* flow and names the file only for `RowsErr` (`FL-016:234`). The
flows index says so itself, under its own list of what is not covered: "A test
double answers a statement — `crud/crudtest/`: the recorder is how almost every
unit test in the tree asserts on SQL, so its statement queue, scan conversion
rules and `Begin` behaviour are worth writing down once. [[UC-011]] is about it
and points at flows that only use it" (`Index.md:284-289`). Half the corrections
in this sweep — the `Exec`/`Query` split, the per-verb queue arithmetic, the
dialect fork — are flow material with nowhere to go.

## The DX this should have

### The call site

```go
func TestGetByIDSelectsByKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.RowsOf[User](crudtest.Vals{
		"ID": 1, "Email": "ann@x.io",
	}))

	if _, err := Users.Bind(rec).GetByID(t.Context(), 1); err != nil {
		t.Fatal(err)
	}

	crudtest.WantSQL(t, rec, 0,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" WHERE "id" = $1 LIMIT 1`)
	crudtest.WantArgs(t, rec, 0, int64(1))
}
```

Ten body lines, against nine today. **The call site does not get shorter, and
claiming it does would be the wrong argument.** What disappears is the helpers
every suite writes before it can write this one — `userRow`, `mustSQL`,
`wantSQL` at `crud/sqlrepo/repository_test.go:47-67` — and the *silent* half of
H-CRUDTEST-04: a row keyed by field name cannot land in the wrong field when two
same-typed fields swap places. It does **not** close the added-column case; that
one stays loud and manual, and pretending otherwise was round one's mistake.

`WantSQL` and `WantArgs` are two calls rather than one variadic, because one
variadic cannot tell "this statement binds nothing" from "I am not asserting on
arguments" — and the vacuous reading is the silent one, which is the defect this
whole file is organised around. The pair costs a line and removes the ambiguity.

Four things the shape must state in its own doc comment or it is worse than what
it replaces:

- **What key `Vals` is in.** The model's **field** name, which is what a
  consumer already types in `crud.Where("TenantID", …)` and in a metamodel — not
  the column, which differs on every model that uses a `db` tag. A key matching
  neither is a `t.Fatalf` naming the model and the unknown key. A silently
  ignored typo is a zero-valued column and a green test, which is the row-literal
  problem back with nicer syntax.
  Promoted embedded fields are not an exception: `RowsOf` accepts a canonical
  full Go-field path and refuses an ambiguous promoted name.  It never guesses
  from an SQL column label.  If two model fields map to the same SQL column
  name, `RowsOf` also refuses the model: only `Cols(...).Rows(...)`, whose
  positional column list is explicit, can express that deliberately irregular
  result.
- **What an omitted column gets.** A pointer or a `crud.Opt` column defaults to
  NULL, a scalar to the Go zero. The two are different values and the difference
  is the whole of [[UC-003]] — a `crud.Opt[int]` filled as `Set(0)` rather than
  null turns the absent-versus-null tests into the thing they exist to catch.
  `crudtest.Zero` asks for the Go zero where NULL is the default.
- **Which column list it fills, and the escape hatch when that is the wrong
  one.** It fills the model's, primary key first then declaration order
  (`crud/meta.go:266-267`). H-CRUDTEST-12 and H-CRUDTEST-15 are the four shapes
  where that is wrong: a `crud.Select` projection, a `Count` (one column), an
  `Aggregate` (groupings plus aggregates), and a many-to-many preload's second
  statement, which carries the owner key ahead of the target's columns. `RowsOf`
  refuses those loudly rather than padding — and hands the consumer a named path
  rather than dropping them back to positional `[]any`:

  ```go
  crudtest.Cols("owner_id", "id", "body").Rows(crudtest.Vals{"owner_id": 7, "id": 1, "body": "hi"})
  ```
- **How arguments are compared.** `1` against `int64(1)` is the one that bites on
  day one. `crudtest.Any` covers a slot the test cannot predict — a
  database-generated uuid arriving through a `RETURNING`, or a column whose
  default the engine fills — so one unpredictable argument does not send the
  consumer back to hand-comparing the slice. (This library stamps no timestamp
  itself: `crud.NowFunc` has exactly one use site, the soft-delete tombstone at
  `crud/sqlrepo/repository.go:936`, and that one is settable. Round one argued
  this case from a clock the library does not touch.)

### Turning one knob

```go
rec := crudtest.Postgres().Lenient()   // strict is the default: a read nothing was
                                       // queued for panics HERE, naming the statement

crudtest.DrainedAtEnd(t, rec)          // and a t.Cleanup that fails if answers went unused,
                                       // skipped when the test has already failed

rec.Fail(driverErr)                    // the next statement fails, Exec or Query, whichever it turns out to be

for _, tx := range rec.Txs() {         // []TxRecord{Begun, End}  — End is TxOpen | TxCommitted | TxRolledBack
	...                                // Statement.Tx indexes into Txs(); -1 outside any transaction
}

clause := crudtest.WhereOf(rec.Last().SQL)   // cut at " WHERE ", trimmed at ORDER BY / LIMIT / OFFSET
```

**Strict is the default, and the sketch says so.** Round one showed
`Strict(t)` — opt-in — while arguing at length for strict-by-default, and an
owner would have implemented the opt-in version believing they had taken the
recommendation. The knob is `Lenient()`, and it exists for the one honest use:
a test that deliberately over-queues because it is asserting on the queue.
Precisely: it changes only an *unqueued `Query`* from a loud failure to the
current empty `rows` result. It does not suppress a queued `Result.Err` or
`RowsErr`, relax scan arity, consume or ignore queued answers, alter `Exec`, or
disable `DrainedAtEnd`; statements are still recorded. Anything broader would
make `Lenient` a second, silent recorder mode rather than a narrow compatibility
escape hatch.

**Strict takes no `testing.TB`, and it does not need one.** A `Query` error
propagates to the caller unchanged (`crud/sqlrepo/repository.go:149-152`;
`crud.ErrNotFound` is only reached on the nil-error, zero-item path at
`:153-157`), so an unqueued read can panic with the statement text and
the consumer's own `t.Fatal(err)` or the test binary prints it at the right
place. That matters for the shape this file holds up as the best example in the
tree: H-CRUDTEST-03 is two `Example_` functions, and an `Example` has no `TB` to
hand anything. Only the drained-queue check needs a hook, so it is a separate
call — and it takes a two-method local interface (`Helper`, `Fatalf`,
`Cleanup`), not `testing.TB`, so `crud/crudtest` does not pull `testing` into
the import graph of a package that is already imported from a non-test file
(`_examples/example/example.go`).

**`Fail` is widened rather than joined by a `FailNext`.** Two one-shot failure
verbs differing only in which call they catch is a knob a consumer has to know
the dialect to turn. Both existing call sites want the wider behaviour: one is
the recorder's own test (`recorder_test.go:192`) and the other is
`crud/decorators/faults/faults_test.go:62`, which arms `Fail` and then pushes
four spare `Result{Err}` precisely because `Fail` is `Exec`-only — and whose
comment says so.

**What a failed assertion prints** is the product here, and it needs specifying
before it is written. A two-hundred-character `SELECT` diffed as two long
strings is unreadable, which is how a consumer's hand-written comparison already
fails them. `WantSQL` prints the first differing token with the surrounding
twenty characters of each side; a count mismatch prints the whole recording, one
statement per line, because at that point the useful information is what *did*
run.

**There is no `EachDialect`.** A shared closure cannot express the matrix it
would exist for: a save is one `Query` with `RETURNING` on PostgreSQL and an
`Exec` plus a read-back on MySQL, so the queue, the write result and the
expected text all differ per arm. The three-line table each arm builds for
itself is the honest shape, and the library's own dialect tests are already
written that way (`crud/sqlrepo/repository_test.go:438-460`).

And for the layer above, where the statement is not the interesting output:

```go
repo := crudtest.RepoLike(Widgets).                          // all three type parameters infer from the blueprint
	OnSave(func(w *Widget) error { w.ID = 7; return nil }).   // so a POST answers a body with an id
	FailOn(crudtest.MethodUpdate, crud.ErrNotFound)           // Get still succeeds; the PATCH does not

h := crudnet.New[Widget, int64, WidgetUpdate](repo)
// ... httptest, in the binding's own package ...
crudtest.WantCall(t, repo.Calls()[0], crudtest.Call{
	Method: crudtest.MethodGet, Sort: []string{"-Price"}, Preloads: []string{"Owner"},
	Filter: crudtest.Any,
})
```

`RepoLike` takes the declared blueprint — or any `port.Repository` — so all
three type parameters and the table name infer from it, the way [[D-022]]
already argues for `New[M, ID, U](repo …)`. Re-spelling `Repo[Widget, int64,
WidgetUpdate]("widgets")` in every test file at the twentieth resource is the
thing every other constructor in this library avoids. It is also not a
constraint: `crud.NewMeta` documents an empty table name falling back to the
snake_case plural (`crud/meta.go:451-462`), so the round-one claim that the name
is mandatory was wrong.

`Calls()` rather than `Last()` alone, because the assertion a handler test
actually makes is over a load-then-write pair and `Last()` cannot see the load —
all four in-tree fakes keep a `calls` slice for exactly this. `FailOn` rather
than one `Err` field, with a documented scope (every call to that method) and a
`FailOnce` beside it, because the commonest handler error test needs `Get` to
succeed and `Update` to fail. `Call` compares **every** field, with
`crudtest.Any` for a slot deliberately not pinned — a partially-filled struct
where a zero value means "don't care" would make a dropped filter, the bug this
layer exists to catch, read as an unset field. And it is chainable like the
recorder rather than a mix of exported fields and methods: one style per package,
and assigning `repo.OnSave` after the handler is mounted is a data race the field
form invites.

### Why this shape

The queue is where the cost is, and it is the only part of this module that
makes a consumer know something they should not have to know. Queueing by
position means knowing how many statements a call will issue, in what order,
whether each one is an `Exec` (which consumes nothing) or a `Query` (which
consumes one), and — since a short page skips its count, a shared preload prefix
folds two terms into one statement, and a `SkipTotal` page fetches `limit+1` —
what the answers you queued will do to that plan. Get it wrong and nothing says
so. Strict-by-default and the drained check turn both directions into a named
failure at the point of the mistake.

**The release-scope decision is to retain the existing split queue for the
additive helpers; a single ordered queue is a separate pre-tag compatibility
decision.** `Exec` returns a sticky field and consumes nothing; `Query` pops a queue.
That single split is what produces blockers 3, 5 and 8, what makes the number of
queued answers dialect-dependent, and what makes `Fail` and a queued
`Result{Err}` two different verbs for one idea. One ordered queue both calls
draw from —

```go
rec.Push(crudtest.Rows(...), crudtest.Written(crud.Result{RowsAffected: 1}), crudtest.Rows(...))
```

— makes queued answers equal statements on every dialect, makes `Exec` popping a
`Rows` entry (or `Query` popping a `Written`) itself the loud failure, folds
`Fail` and the queued error into one verb, and removes `ExecResult`'s
stickiness. It breaks `ExecResult`'s semantics, which is exactly the cost the
compatibility argument below covers. Do not ship `Written`, a widened `Fail`, or
queue-aware assertion helpers that imply the replacement until its exact
migration (including every existing `ExecResult`/`Fail` caller) is approved.
The owner should make that D-020-compatible decision before the tag; it is not
silently included in this module's helper proposal.

The assertion helper is not sugar, and the evidence is worth stating exactly
because round one's arithmetic did not add up. **Twenty-four named local helpers
across sixteen files**, and they partition cleanly:

- **ten** render a `crud.Predicate` or a `[]crud.Order` to SQL through
  `crud.NewSQL` — `predSQL` ×3 (`crud/http/crudnet/fake_test.go:303`,
  `crudgin:318`, `crudfiber:312`), `mustRender` and `orderSQL`
  (`crud/predicate_test.go:14`, `:227`), `clause` ×3
  (`crud/query/compile_test.go:39`, `crud/rpc/crudgrpc/client_test.go:37`,
  `remote/roundtrip_test.go:24`), `render`
  (`crud/query/roundtrip_test.go:18`), `commentScope` (`crud/scope_test.go:19`);
- **three** wrap those to take a `*crud.Options` — `whereSQL` ×3;
- **four** cut a clause out of statement text, in two behaviours that disagree
  (H-CRUDTEST-10);
- **two** run a recorder and hand back the statement or its clause — `sqlOf`
  (`_examples/example/blog/blog_test.go:21`) and `where`
  (`crud/decorators/specs/specs_test.go:51`);
- **three** build a row literal for a model — `userRow`, and `row` twice;
- **two** are the assertion and the indexed accessor — `wantSQL` and `mustSQL`
  (`crud/sqlrepo/repository_test.go:62`, `:53`).

So the shipped set is three names, and each one has call sites that name it:
`WantSQL`/`WantArgs` and `WantStatements` close the last group and the
whole-recording assertion H-CRUDTEST-15 and H-CRUDTEST-16 both ask for;
`WhereOf` closes the third; and the first two groups are one pair —
`RenderPredicate(d, m, p)` for the ten, `RenderSQL(d, m, opts...)` for the three
that take options. A single options-shaped function would have replaced none of
the ten without every caller first wrapping its predicate in `crud.Where(...)`,
which is what round one proposed.

`WantStatements(t, rec, want ...string)` matters more than its line count
suggests: it asserts the text, the order and **the count** in one call.
Statement-by-index is fine for a single-statement verb and puts the consumer
straight back into counting for a paginated read, a preload or a MySQL save —
the exact knowledge the paragraph above says they should not need.

`RepoLike` is the same argument one layer up, with the scope caveat in
H-CRUDTEST-18.

### What it must not break

- **[[D-041]]** forbids giving the recorder a `DataSource()`, and the reason is
  not tidiness: it silently rescopes `crud.InTx` in every test that wraps a
  recorder. Nothing proposed here adds one, and `TestTheRecorderStaysUnidentified`
  (`recorder_test.go:457`) is the guard that would catch it — with its control at
  `:503`.
- **[[D-048]] and `scripts/checks.sh:TIER0`** — `crud/crudtest` may import `crud`,
  `crud/query`, `errs`, `errs/sqlerr`, `port`, `port/porthttp` and the standard
  library, and nothing else. `RepoLike` is legal because `port` is on the
  manifest; `crud.MustSchemaOf[M]` is exported and in `crud`, so `RowsOf` is
  implementable where it is proposed. The local `TB` interface means `testing`
  never enters the graph at all, so the tier argument does not have to carry a
  design decision. Two things are **not** legal here and this proposal does not
  smuggle either in: anything that names a binding's `Option` type, and anything
  that touches `crud/catalog`.
- **[[D-014]]** is what the whole assertion style rests on. `WantSQL` may
  collapse whitespace and nothing else. A comparison that "understood" SQL would
  spend the property D-014 bought and hide exactly the rendering changes these
  tests exist to catch.
- **[[D-019]], and this proposal stands on its side.** D-019's invariant is that
  a dialect difference is not observable except in eleven named places, and its
  first compensation row — "MySQL has no `RETURNING` … one extra round trip" —
  is precisely the cost that surfaces here as a queue count. That is difference 1
  leaking into a *test* API, not a twelfth observable difference in the
  repository API, so it needs no D-019 amendment. What it needs is saying out
  loud in the module doc, and D-019 already cites `TestDialectShorthands` under
  **Proven by**, which is the natural place to hang it.
- **[[D-020]] argues for strict, not against it.** Its invariant is that a test
  which could pass without the behaviour it names must sit beside a control, and
  an unqueued read answering empty is precisely the vacuous pass it exists to
  forbid. Round one cited D-020 as the reason `Strict` must be opt-in; that was
  backwards. The only real argument for opt-in is compatibility, and there is
  none to protect yet. The countable cost is knowable today: there are **323
  recorder constructions** across the test files, **177** of which chain a `Push`
  on the constructor line; the rest either queue later, queue nothing because the
  verb is an `Exec`, or read against a drained queue on purpose. That last group
  is the one strict-by-default reddens, and it is a one-time sweep — after
  `v0.1.0` the same change costs a major version. **The cheap moment is now**,
  which is the roadmap's own phrasing for four other items.
- **[[D-022]] remains a decision challenge, not a justification by itself.** A
  `RepoLike` convenience double is compatible with its narrow-interface goal
  only if it stays optional and does not turn `port.Repository` into a larger
  required test surface; the owner must resolve that tension explicitly.
- **[[D-041]] remains non-negotiable for both queue choices.** Neither an
  ordered queue nor `RepoLike` may add `DataSource()` or change the way
  `crud.InTx` identifies a source. [[D-048]] likewise constrains the proposed
  helpers to the existing import manifest; it rules out importing a binding or
  test-only package to make their assertions convenient.
- **[[D-020]] rule 3 is scoped and the module doc should say so.** "No
  `t.Parallel()`. There are zero calls to it in the tree" is a rule for *this*
  repository, and its stated reason is shared physical tables in
  `test/integration`. A consumer's package has no such rule and their default is
  `-race`. H-CRUDTEST-19 is what has to be written down.
- **[[UC-011]] Out of scope** currently refuses a shipped handler double and
  refuses round-tripping. This file challenges the first (H-CRUDTEST-18) and
  agrees with the second, and asks that the second move somewhere a consumer will
  read it: `docs/modules/en/crudtest.md`'s "What it will not tell you" is about
  SQL validity and constraints and says nothing about the queue not being a store.
- **[[D-022]]** is why a shipped `RepoLike` is possible at all, and it also
  states the opposite intent in as many words: the interface is narrow so that
  it is a small thing to implement by hand. That is a real counter-argument and
  the owner should weigh it against four in-tree copies, three of which
  independently grew the same two behaviour hooks.
- **[[D-031]]**'s **Proven by** section is integration-only. The unit test
  H-CRUDTEST-07 asks for belongs in it, and the existing security subtest that
  half-proves the stamp wants its assertion strengthened in the same change.
- `Txs()` needs one additive field on the exported `Statement` — `Tx int`, `-1`
  outside any transaction, so "no transaction" cannot be confused with "the
  first one". That will show up in `make api`. It is additive, so no existing
  literal or assertion breaks — worth saying, because the whole argument for
  `Txs()` over recording `BEGIN`/`COMMIT` is cost. `TxRecord` carries one `End`
  field rather than two independent bools, so "both true" is unrepresentable and
  "still open" — the state `TestTransactionRollsBackOnError` cannot currently
  tell from a rollback — is a value a test can name.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| A read nobody answered is loud | silent empty result set, and the leftover answers the next read | large |
| Observable commit and rollback | not observable; the fields are set on an unexported type and nothing can read them (`recorder.go:169-176`) | large |
| Rows keyed by field name | positional `[]any` in schema order; three suites grew their own `row()` builder, every added column rewrites them, and a reordered field rewrites nothing and breaks everything | large |
| One assertion call | `Normalize` plus a hand-written comparison; two of twenty-four local helpers are the assertion and the accessor | large |
| Assert the whole recording in order | `len(rec.Statements())` plus a loop, written from scratch every time; nothing suggests writing it | large |
| Fail the next statement, whichever call it is | `Fail` for writes, `Push(Result{Err})` for reads, count unknown — the tree's answer is `Fail` plus four spare error results and a comment (`faults_test.go:57-67`) | large |
| Render compiled options or a predicate as SQL | thirteen local helpers, ten of them the same six lines over `crud.NewSQL` | large |
| A repository double for handler and service tests | about 160 lines to write; the four in-tree copies disagree with each other on hooks and inspectors | large |
| A mid-stream read failure | `RowsFailing` exists, is well designed and well documented, and has one use site outside its own test | none in code, small in docs |
| A driver error the library classifies, through a recorder-backed repository | reachable, but only by writing a ten-line `crud.Source` that calls `sqlfault.Wrap`; nothing demonstrates it | small in code, absent in docs |
| Cut the clause out of a statement | four named helpers in two behaviours that disagree, plus three inline copies | small each, and nobody stops writing it |
| `Reset` that resets | clears two of five fields while the doc says "clear everything"; its own test exercises only the two that work | small |
| Know whether a verb is an `Exec` or a `Query` | `Statement.Query` answers it after the fact and is named in no consumer-facing document | none in code, large in docs |
| Know what arity a non-model read wants | four different answers, none written down; `Aggregate` has no worked example at all | none in code, large in docs |
| One word per shipped dialect, defaults stated | `Postgres()`, `MySQL()`, `New(crud.SQLite{})` — and `MySQL()` silently means `RowAlias: false` | small in code, sharp in docs |
| Safe under `-race` and `t.Parallel()` | the recorder is; `crud.NowFunc` is not, and the case that recommends swapping it does not say so | none in code, large in docs |
| A dialect loop | write the table; three lines, and this is the right shape | none |
| A runnable example on the package page | none in the package; the examples live in `_examples/`, which the toolchain ignores and pkg.go.dev will never render | small |
| A flow that documents the queue | none — the reverse index points `recorder.go` at the catalog flow | small, and it is where six other fixes belong |

**Overall:** the short path is genuinely short — one word for the source, one
call to bind, and the statement is right there — and **forty-one test files** in
this repository lean on it, which is stronger evidence that it works than any
argument here. It gets wordy the moment a test is about more than one statement:
the queue is positional, the number of entries is knowledge the consumer acquires
by reading the implementation, and every mistake in it is silent. Customising
does not mean abandoning the short path, because there is barely anything to
customise — nine top-level names (`docs/api/surface.md:254-265`) — and the things
a consumer reaches for next are not further along the path, they are absent from
it. The distance that matters most is not code: eight of the nineteen rows above
are a document that is wrong, a document that does not exist, or a document with
nowhere to live.

## Release blockers found here

Rows 1 and 2 change price at the tag and should be settled before it. Rows 3 to
7 are documents and cost an afternoon between them. Rows 8 to 13 are release-note
material.

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | An unqueued read answers empty rather than failing, and an unconsumed answer stays queued for the next read (`recorder.go:147-159`) | blocker | Both failures are green tests. The first suite a consumer writes contains the 404 test this renders meaningless — `TestGetByIDNotFound` passes with its `Push` deleted. Strict-by-default costs one sweep of 323 in-tree call sites now and a major version after the tag. [[UC-011]]'s Status already records it in almost these words and must be edited in the same change. |
| 2 | Whether the rendered SQL is stable across releases is undecided | blocker | A team pins four hundred statement strings and finds out at the first `go get -u`. [[D-014]] promises byte-identical SQL within a build and says nothing about two versions. The decision belongs to `crud/query`; what this module owes is one sentence under either answer, and both are cheap only while there is no tag. |
| 3 | `ExecResult` is sticky but documented as one-shot (`recorder.go:81-86` against `docs/modules/en/crudtest.md:53`, which sits directly above `Fail`, which really is one-shot) | serious | **Doc edit.** A consumer sets `RowsAffected: 1` for an insert and every later `Exec` in the test reports 1 too — including the zero-rows branch of an optimistic-lock refusal, which is the exact case `version_test.go:84` sets it for. |
| 4 | `Reset` clears statements and queue only; exec result, pending failure and transaction count survive, and the doc says "clear everything" (`recorder.go:121-126`, `docs/modules/en/crudtest.md:75`) | serious | **One line plus a doc edit.** An armed `Fail` crossing a subtest boundary fails a statement in a test that never asked for one. Its own test exercises only the two fields that work, so a control would fail today. |
| 5 | The module doc's "Queue as many results as the call will make" (`:47-48`) is wrong for every `Exec`-shaped statement, and `Statement.Query` — which answers the question — is named nowhere a consumer reads | serious | **Doc edit.** It sends a MySQL insert test to queue a row the read-back then eats, and the resulting failure names none of that. There is no flow to put the corrected version in (see below). |
| 6 | A commit and a rollback are unobservable — the fields are set on an unexported type and nothing can read them (`recorder.go:169-176`) | serious | [[UC-011]] guarantee 11 promises "a test asserts the begin, the statements and the commit in one place". The library's own proxy (`repository_test.go:582-597`) asserts `TxDepth() == 1` and would pass for a transaction left open. A shipped guarantee the code cannot satisfy. |
| 7 | `crud/crudtest` has no flow; the reverse index maps `recorder.go` to FL-016, the catalog flow (`docs/ai/flows/Index.md:127`, and the gap is named at `:284-289`) | serious | Half the corrections in this sweep are flow material — the `Exec`/`Query` split, the per-verb queue arithmetic, the dialect fork — and there is nowhere to put them. CLAUDE.md's lookup order starts at the flows index, so an agent asked to fix rows 3 to 5 lands on the catalog. The nearest thing that exists has drifted: FL-001 step 8 states the four COUNT cases correctly and points at `repository.go:152-168` and `repository.go:392`, which are now `:204-227` and `:559`. |
| 8 | No repository double is exported; four have been written inside this repository and never shipped | serious, and a scope question | Every consumer testing a handler or a service re-derives about 160 lines, and the four in-tree copies disagree on hooks and inspectors. [[UC-011]] files this under Out of scope while its Status calls it "the gap" and `docs/ai/usecases/Index.md:217` carries it as open tension 9 — **three places** need reconciling, whichever way it goes. |
| 9 | Nothing says the queue is not a store | serious | Save-then-read does not round-trip. [[UC-011]] says so under Out of scope; the module doc's "What it will not tell you" does not, and that is the page a consumer reads. Same failure class as row 1: a green test over a model the double never saw. |
| 10 | Cursor pagination has no unit-level example anywhere, and three of its rules are silent (`repository.go:171-192`, `:245-253`) | serious | A `SkipTotal` page fetches `limit+1`, so queueing exactly `limit` rows to prove "there is more" asserts the opposite and passes. Cursors are emitted only for a unique sort, silently. Every cursor test in the tree is integration-only. |
| 11 | `Fail` covers writes only; a failed read is a queued `Result{Err}`, and which one a verb uses depends on the dialect | sharp edge | The tree's own workaround arms both and pushes four spare error results (`faults_test.go:57-67`), which then fail the next four reads — so the one-shot guarantee and the catch-either-call need cannot be had together. Both existing call sites want the wider verb. |
| 12 | `crudtest.MySQL()` bakes in `RowAlias: false` and nothing says so (`recorder.go:68`, `crud/dialect.go:126-153`) | sharp edge | An application on MySQL 8.0.19+ declares `RowAlias: true` in production and pins the MariaDB upsert spelling in its tests. The divergence is live here — two integration tests use `RowAlias: true` — and no recorder-backed test does. One sentence next to the shorthands. |
| 13 | `TxDepth` counts begins and never decrements while the doc calls it nesting depth (`recorder.go:128-133`, `docs/modules/en/crudtest.md:74`) | sharp edge | **Doc edit**, and [[UC-011]] guarantee 11 already has the right wording — "the number of transactions opened is readable". Two sequential transactions read as depth 2, so the assertion a consumer writes from the module doc's sentence fails for an unrelated reason. |
| 14 | No assertion helper, no whole-recording assertion, no clause extractor, no predicate renderer | sharp edge | Twenty-four local definitions across sixteen files here; every consumer writes the first three again, with worse failure messages than this package could print. The clause extractor exists in two incompatible behaviours already. |
| 15 | Nothing says whether a recorder is safe to share, and `crud.NowFunc` is process-global | sharp edge | The recorder is mutex-guarded and `Statements()` copies, so the answer is "yes, but one per test anyway". H-CRUDTEST-07 tells a consumer to swap a package-level `var` and does not say it is one. `-race` plus `t.Parallel()` is the default in most shops. |
| 16 | No runnable example in the package | sharp edge | At the tag the package page shows nine names and no example, for a package whose only audience is people reading it to learn how to write a test. The examples live in `_examples/`, which pkg.go.dev will never render. |

## Contested

- **A `Faults(errs.Classifier)` option on the recorder is not "a few lines", and
  I did not propose it.** Round one's dx review was right that the classifier is
  public and pure, and that finding is adopted in H-CRUDTEST-13. But the recorder
  cannot call `sqlfault.Wrap`: `crud/sqlfault` is not on `scripts/checks.sh:TIER0` and
  `make check-tiers` fails the moment `crud/crudtest` imports it. An option that
  called `Classify` directly would skip the integrity gate [[D-038]] puts after
  it, and a double that classifies *differently* from the adapter it doubles is
  worse than one that does not classify at all.
- **Blocker 8 stays a release blocker.** Reviewers are right that [[UC-011]]
  files the shipped double under Out of scope, and that is now said in
  H-CRUDTEST-18 and in the row. It stays because the same use case's Status calls
  it "the gap" and the usecase index carries it as an open tension: the
  contradiction is inside UC-011, in three places, and a tag that ships it
  unresolved ships a use case that disagrees with itself.
- **H-CRUDTEST-19 (parallel and `-race`) stays, despite [[D-020]] rule 3.** A
  reviewer could reasonably say the repository already forbids `t.Parallel()`, so
  there is nothing to answer. D-020's stated reason is shared physical tables in
  `test/integration` plus a preference for a uniform rule, and neither reaches a
  consumer's own package. The answers happen to be favourable — the recorder is
  guarded, `Statements()` copies — which is exactly why saying them costs three
  sentences and not saying them costs somebody a bug report.
- **Blocker 12 is kept, and it argues *against* part of round one's blocker 12.**
  The dx review is right that every one-word shorthand hiding a dialect field is
  another silent variant mismatch, and `crudtest.MySQL()` is the live example. So
  the "ship a `SQLite()` shorthand" half is withdrawn — SQLite has no options, so
  it is harmless, but it is not a blocker — and what remains is the `MySQL()`
  default, which is. The missing package example is unrelated and stays as row 16.
- **H-CRUDTEST-16's batched-insert gap is named here rather than moved out.**
  Reviewers are right that "no test in `crud/sqlrepo` asserts the batched
  insert's shape" is a `crud/sqlrepo` coverage gap. The case is kept because the
  consumer story — "prove eight thousand rows are one statement" — is the single
  most common reason someone reaches for this module, and it produces one
  crudtest action: `WantStatements`, which is in the proposal. The missing test
  itself is filed against `crud/sqlrepo`.

## Edge cases

### E-CRUDTEST-01 — Normalising a statement containing meaningful whitespace
**Shape:** adversarial input · boundary
**Setup:** A statement or expected fixture contains a string, quoted identifier, or dialect expression whose value includes two spaces, a tab, or a newline.
**What the consumer does:** They use `crudtest.Normalize` to ignore builder formatting while asserting the literal content their query must preserve.
**What must happen:** Normalisation changes whitespace between SQL tokens only; it never makes two statements with different literal contents compare equal.
**Today:** ❌ wrong or unhandled
**Evidence:** `Normalize` applies `strings.Fields` to the entire SQL string and rejoins it (`crud/crudtest/recorder.go:246-248`), which also collapses whitespace inside quoted literals and identifiers. Its only test contains token whitespace only (`crud/crudtest/recorder_test.go:436-443`), while 23 in-tree tests use the helper; no literal-whitespace control was found.
**Blast radius:** silent wrong answer

### E-CRUDTEST-02 — A recorder is declared without a dialect
**Shape:** degenerate declaration
**Setup:** A test creates `crudtest.New(nil)`, or uses a zero-value `Recorder`, after a table-driven dialect arm was left uninitialised.
**What the consumer does:** They bind the repository, expecting a configuration error at the test's setup line.
**What must happen:** A recorder that cannot render SQL rejects construction loudly, including interface-typed nil dialects.
**Today:** ❌ wrong or unhandled
**Evidence:** `New` stores any `crud.Dialect` without validation and `Recorder.Dialect` returns it unchanged (`crud/crudtest/recorder.go:51-70`). Repository binding immediately calls dialect methods such as `Quote`, `SupportsReturning`, and `Upsert` (`crud/sqlrepo/repository.go:31-60`), so nil instead fails later as a panic. `TestDialectShorthands` covers only three concrete values (`crud/crudtest/recorder_test.go:418-434`); no nil or typed-nil dialect test was found.
**Blast radius:** crash

### E-CRUDTEST-03 — A typed-nil `sql.Scanner` is a scan destination
**Shape:** misuse
**Setup:** A custom scanner is a nil pointer held in an `any` destination slice because a fixture's optional value was not allocated.
**What the consumer does:** They expect the recorder to return its ordinary "not a non-nil pointer" scan error, so the test fails at the fixture boundary.
**What must happen:** Typed-nil scanners are rejected before their method is invoked.
**Today:** ❌ wrong or unhandled
**Evidence:** `assign` invokes `sql.Scanner.Scan` before it checks whether the reflected destination is a non-nil pointer (`crud/crudtest/recorder.go:209-217`), so a typed-nil scanner reaches its own method and can panic. `TestScanRefusesRowsItCannotFill` tests a non-pointer destination, not a typed-nil scanner (`crud/crudtest/recorder_test.go:292-330`); no typed-nil scanner test was found.
**Blast radius:** crash

### E-CRUDTEST-04 — A query answer moves through its complete consumption state machine
**Shape:** partial failure · seam
**Setup:** A pagination test queues, in order, an immediate `Result{Err: refused}`, `RowsFailing(late, row)`, and a successful retry result.
**What the consumer does:** It makes the refused query, inspects its recorded attempt, then iterates the live cursor and finally performs the retry. It must not have to infer whether an error consumed an answer or when a cursor error becomes observable.
**What must happen:** Each `Query` records one `Query` statement and consumes exactly one queued `Result`, whether it immediately refuses or returns rows. `Result.Err` returns from `Query`; `RowsErr` remains nil from `Rows.Err` until `Next` has exhausted the configured rows, then becomes the terminal error; the following `Query` receives the following result. This is one cursor/queue contract, not four unrelated cases.
**Today:** ❌ wrong or unhandled
**Evidence:** `Push` appends result slots in order (`crud/crudtest/recorder.go:72-78`), and `Query` records, removes the first slot, then checks its immediate error (`crud/crudtest/recorder.go:147-157`), so immediate refusal already consumes and leaves a trace. `Next` yields configured rows until exhaustion (`crud/crudtest/recorder.go:185-192`), but `Err` returns `RowsErr` at every cursor position (`crud/crudtest/recorder.go:194`), violating the required late-error state. The immediate-error test asserts only one call (`crud/crudtest/recorder_test.go:119-130`) and the late-error test calls `Err` only after its loop (`crud/crudtest/recorder_test.go:132-177`); neither pins the complete transition sequence.
**Blast radius:** confusing error

### E-CRUDTEST-05 — A queued fixture is edited after `Push`
**Shape:** concurrency · misuse
**Setup:** A test builds a `[]any` row, queues it, then reuses or changes the slice while preparing a second request or subtest.
**What the consumer does:** They need to know whether the fixture is a snapshot or a deliberately live test value before reusing it.
**What must happen:** The API either snapshots rows at `Push`, or documents this as a sharp, caller-owned-fixture API and offers an explicit snapshot helper for tests that need isolation.
**Today:** 🟡 partial (sharp API, not a documented snapshot promise)
**Evidence:** `Rows` retains the caller's row slices (`crud/crudtest/recorder.go:42-49`) and `Push` appends `Result` values without copying their rows (`crud/crudtest/recorder.go:72-78`); `Query` returns those same row slices to `rows` (`crud/crudtest/recorder.go:147-159`). The queue-order test uses immutable literals (`crud/crudtest/recorder_test.go:81-117`), and no mutation or race test for queued fixtures was found.
**Blast radius:** silent wrong answer

### E-CRUDTEST-06 — The caller reuses a mutable bound argument after `Exec`
**Shape:** concurrency · seam
**Setup:** A repository passes a mutable `[]byte` or a caller-supplied `[]any` argument slice to the recorder, then reuses the buffer before the assertion reads the statement.
**What the consumer does:** They need to know whether a recorded call is a snapshot or a view before reusing buffers.
**What must happen:** The API either snapshots argument slices and mutable values at record time, or labels `Statement.Args` a sharp view and provides an explicit snapshot assertion path.
**Today:** 🟡 partial (sharp API, not a documented snapshot promise)
**Evidence:** `Exec` and `Query` retain the variadic argument slice directly in `Statement.Args` (`crud/crudtest/recorder.go:135-150`), and no deep copy is made. The recording test reads immutable scalar arguments only (`crud/crudtest/recorder_test.go:34-69`); no mutable-argument test was found.
**Blast radius:** silent wrong answer

### E-CRUDTEST-07 — An assertion edits the `Args` returned by `Statements`
**Shape:** misuse · concurrency
**Setup:** A test trims, sorts, or overwrites `rec.Statements()[i].Args` while preparing a comparison.
**What the consumer does:** They need an inspector result they can safely transform for comparison, or an explicit warning that nested values remain shared.
**What must happen:** `Statements` either deep-copies nested `Args` (and documents the limit for mutable values), or its doc comment calls the outer-slice copy a sharp API and exposes a snapshot helper for mutation-safe assertions.
**Today:** 🟡 partial (the outer copy is promised; nested isolation is not)
**Evidence:** `Statements` copies only the outer `[]Statement` slice (`crud/crudtest/recorder.go:96-101`); each copied `Statement.Args` still aliases the recorded slice retained by `Exec` and `Query` (`crud/crudtest/recorder.go:135-150`). `TestStatementsIsACopy` changes `SQL`, a string value, and does not mutate `Args` (`crud/crudtest/recorder_test.go:332-346`).
**Blast radius:** silent wrong answer

### E-CRUDTEST-08 — A result declares both a query failure and a rows failure
**Shape:** degenerate declaration
**Setup:** A test helper constructs `crudtest.Result{Err: immediate, RowsErr: late, Rows: rows}` while combining two failure fixtures.
**What the consumer does:** They expect an invalid fixture to fail where it is declared; one statement cannot both refuse to create rows and yield rows before failing.
**What must happen:** The recorder rejects the contradictory result, or exposes an explicitly documented precedence that cannot make the test silently exercise the wrong branch.
**Today:** ❌ wrong or unhandled
**Evidence:** `Result` exposes both fields at once (`crud/crudtest/recorder.go:27-40`), and `Query` silently returns `Err` first, discarding `Rows` and `RowsErr` (`crud/crudtest/recorder.go:147-159`). `RowsFailing` constructs only the late-error form (`crud/crudtest/recorder.go:45-49`), and the two failure tests exercise one form each (`crud/crudtest/recorder_test.go:119-177`); no contradictory-result test was found.
**Blast radius:** confusing error

### E-CRUDTEST-10 — The test records and inspects ten thousand statements
**Shape:** scale
**Setup:** A generator or property test issues thousands of repository calls and reads `rec.Last()` after each one for a local assertion.
**What the consumer does:** They expect the direct accessor for the most recent statement not to copy the entire history on every call.
**What must happen:** `Last` is constant-size work, or the scale cost is explicit and a no-copy accessor is available.
**Today:** 🟡 partial
**Evidence:** `Last` calls `Statements` (`crud/crudtest/recorder.go:103-110`), and `Statements` copies every recorded statement (`crud/crudtest/recorder.go:96-101`); repeated `Last` calls over a growing log are therefore quadratic in copied entries. Tests cover a fresh recorder and two statements (`crud/crudtest/recorder_test.go:34-79`), not a large recording.
**Blast radius:** confusing error

### E-CRUDTEST-13 — Cancellation or deadline must not become a false-green fake call
**Shape:** cancellation · boundary
**Setup:** A repository test passes an already-cancelled context, or lets a deadline expire, immediately before `Begin`, `Exec`, or `Query`.
**What the consumer does:** They assert the same operation refuses with `ctx.Err()` and that no fake work was recorded or consumed; otherwise a cancellation test can pass only because the recorder ignores the context.
**What must happen:** Before changing recorder state, all three methods return the context error. Cancelled `Exec`/`Query` add no statement and consume no queued answer; cancelled `Begin` returns no transaction and leaves `TxDepth` unchanged. The fake cannot represent an in-flight cancellation without an explicit, separately designed hook.
**Today:** ❌ wrong or unhandled
**Evidence:** `Exec` and `Query` name but discard their `context.Context` parameter and proceed to record (and `Query` to consume a result) (`crud/crudtest/recorder.go:135-159`); `Begin` likewise ignores it and increments `txDepth` (`crud/crudtest/recorder.go:161-167`). No context cancellation check exists in those methods.
**Blast radius:** silent wrong answer

## Edge verdict

The recorder can inject both immediate and terminal read failures, but its queue/cursor contract is not pinned as one state machine: a query does consume and record an immediate refusal, while `Rows.Err` exposes a configured terminal error too early. Its sharpest false-green defects are `Normalize`, which erases meaningful literal whitespace, and ignored contexts in `Begin`/`Exec`/`Query`, which let a cancellation test run fake work. Rows, arguments, and nested inspector values are shallow sharp APIs today — the outer `Statements` slice is copied, but neither construction nor documentation promises a deep snapshot. Invalid setup also reaches request-time behaviour — nil dialects, typed-nil scanners, and contradictory result errors — instead of failing at the line that declared the double.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `Normalize` collapses whitespace inside quoted SQL content (`crud/crudtest/recorder.go:246-248`) | serious | The helper advertised for stable statement assertions can make two semantically different statements compare equal, producing a green test over the wrong SQL. |
| 2 | Queued rows and recorded arguments remain mutable through caller-owned slices and `Statements()` (`crud/crudtest/recorder.go:72-78`, `crud/crudtest/recorder.go:96-101`, `crud/crudtest/recorder.go:135-159`) | sharp edge | The outer `Statements` slice is copied, but nested data is not a promised snapshot; consumers need either a documented sharp boundary or an explicit snapshot helper before relying on mutation-safe assertions. |
| 3 | `New(nil)` and typed-nil dialects pass setup and panic only when a repository renders SQL (`crud/crudtest/recorder.go:63-70`) | serious | A table-driven test fails far from the missing dialect, often while constructing the repository under test rather than at its fixture declaration. |
| 4 | `Begin`, `Exec`, and `Query` ignore cancelled/deadline contexts (`crud/crudtest/recorder.go:135-167`) | serious | A recorder-backed cancellation test can pass after the fake records a write, consumes a result, or opens a transaction that a real source should have refused. |
