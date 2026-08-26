# crud/decorators/specs — a filter written in Go as a value you can name, compose and test

**Covers:** `github.com/frostgrove/vv/crud/decorators/specs` (with the metamodel `cmd/vv` writes into `vv_gen.go`)
**Sweep:** happy paths · release readiness
**Verdict:** not ready — two new blockers and one already filed. New: a filter that composes to a tautology walks through *both* bulk-write guards and empties the table, and the pattern operators escape their wildcards but emit no `ESCAPE` clause, which is wrong on SQLite through the typed form and wrong on every dialect through the literal form the module doc teaches first. Already filed, and still a live breach of [[D-015]]: the two refusal sentinels wrap nothing, so a refused bulk write reaches the client as 500. The composition algebra and the find-one contract underneath them are the best-proven code in this repository.

## What a consumer is actually trying to do

Someone has the same filter in three places. The admin list endpoint wants
"active, in this tenant, not archived". The nightly billing job wants the same
thing. The CSV export wants the same thing again. Today it is copy-pasted three
times, and the day somebody adds "and not suspended" it gets added twice.

They want to write it once, give it a name, and pass it around like any other
value — return it from a function, take it as an argument, `and` it with the
caller's own conditions, keep it in a variable while a request handler decides
which halves of a search form were actually filled in. They want a function
called `activeInTenant()` that they can read in a code review.

They also want a rename to hurt at the right time. Filters addressed by string
literals are a slow leak: a column gets renamed, three of the five call sites get
updated, the other two keep compiling and start returning 400s to customers. The
promise that brings people to this module is that a field is an identifier, so
the compiler finds all five.

Then there is the boring middle. A search box that is case-insensitive because
every search box is. Five optional filters where four are usually blank. A list
of ids that sometimes comes back empty, and sometimes comes back with ninety
thousand entries. "Not archived" over a column that is null for everything that
was never archived. A "delete everything not in this set" that has to be safe
when the set is empty. A bulk purge that has to be one transaction with the audit
row beside it. A trash screen with a restore button. An export that streams the
whole filtered set. A money column that is a `type Cents int64` rather than an
`int64`. A test that asserts the filter is right without standing a database up,
and a way to see what that filter compiled to on the night it returns nothing.
None of these are showpieces and all of them happen in the first month.

Many of the people arriving have written Spring Data before. They will try to
port a Java repository layer method for method, and they want to be told early
which of those habits have no equivalent here rather than discovering it one
method at a time.

Finally: they do not want to be captured. The filter should work against the
repository they already have, against an access-control policy, against a
preload, and against a service in another process — without being rewritten each
time, and with the places it *does not* reach said out loud rather than assumed.

## Happy cases

**The grading rule, stated once because round 1 applied two.** A guarantee is ✅
only when a test fails if it stops holding. A property that is true by reading
the code and pinned by nothing is ❓, however obviously true — that is
[[D-020]]'s position and this sweep does not get an exemption from it. Where a
verdict changed between rounds for this reason it says so.

Case numbers are stable across rounds; new cases are appended rather than
inserted, so a round-1 reference still lands.

### H-SPECS-01 — Name a filter once and use it in three places
**Who:** the engineer who owns the "articles" resource and its nightly digest job
**Wants:** `publishedInTenant(t)` to be one function, called from the list handler, the job and the export.
**Story:** They write a function returning a filter value. The handler ANDs the request's own conditions onto it. The job uses it bare. The export uses it with a date range on top. All three run concurrently in the same process.
**Must hold:**
1. The result of combining two filters is itself combinable — `Where(a).And(b)` can be handed to a function whose parameter is a plain filter, without a conversion call.
2. The shared filter can be stored on a struct field and used by two requests at once without one request's `And` being visible to the other.
3. Two conditions over the same column AND to two clauses, not one silently merged clause.
**Today:** ✅ ready
**Evidence:** `spec.go:107` declares `Composite[M]` and `:113` gives it `ToPredicate`, so the value `And` returns satisfies `Specification[M]` — guarantee 1 is the closure, and it is what a fluent builder usually loses. Guarantee 2: `combine` (`spec.go:142-156`) and `fold` (`spec.go:174-190`) each allocate a fresh `SpecFunc` closure over their operands and mutate nothing, and `Predicate` (`spec.go:196`) evaluates against a fresh zero `Root[M]`, so evaluation writes to nothing shared.
Guarantee 3's citation was wrong in round 1 and both reviewers caught it. `specs_test.go:85-97` composes four conditions over four *different* columns (age, name, email, id) and would pass unchanged if same-column conditions were merged; it is the right citation for associativity (H-SPECS-16) and the wrong one here. The test that actually pins this is `edge_test.go:257-274`, which renders `WHERE ("active" = $1 AND "active" = $2)` and asserts the argument order too. Note what it does *not* cover: both conditions arrive on the same column from different sides — one from a repository scope, one from the specification. Nothing pins the range case a consumer writes first, `AllOf(Age.Gte(lo), Age.Lte(hi))`, though [[D-054]] records the merging refusal as deliberate for the wire path.
**If not ready:** n/a for the behaviour. One line added to `TestComposition` putting two conditions on `User_.Age` would close the citation gap for the shape consumers actually write.

### H-SPECS-02 — The admin search form where four of five filters are blank
**Who:** the engineer building an internal back-office list screen
**Wants:** to build one filter from a request struct where each field is optional, and have the blanks contribute nothing.
**Story:** They bind a query struct with `q`, `status`, `since`, `minSpend` and `ownerID`. Most requests set one. They assemble a filter from whichever are present and hand it to the list call. Two of the fields are `*T`, because that is what the generated DTOs in this repository use.
**Must hold:**
1. An absent condition contributes no clause — not a clause that happens to be true.
2. All conditions absent produces a statement with no `WHERE` at all and no bound arguments.
3. A missing operand of an `or` never widens the result to everything.
4. An `in` over an empty slice matches nothing rather than failing to parse.
5. The assembly stays an expression, so it can be returned from a function and nested inside an `AnyOf` without a statement block.
**Today:** 🟡 partial — 1 through 4 hold; 5 does not.
**Evidence:** the semantics are pinned harder than anything else here. `edge_test.go:45` (`TestNoShapeOfEmptySpecificationRestrictsTheQuery`) enumerates thirteen spellings of "empty" and asserts every one compiles to no `WHERE` and no args; `edge_test.go:100` (`TestANilOperandNarrowsToTheOtherSide`) asserts a nil operand narrows to the side that exists. `spec.go:174-190` is the fold that drops nil members — the property a conditional constructor would rest on, and **nothing states it**: `AllOf`'s doc comment (`spec.go:168`) says only "an empty list means no restriction", and the module doc's Composing section (`docs/modules/en/specs.md:152-160`) is silent. Guarantee 4 is `crud/predicate.go:201-209`, which degrades an empty `in` to `1 = 0` — see H-SPECS-20 for what the same node does to `not in`. Guarantee 5 fails: there is no conditional constructor anywhere in the exported surface (`docs/api/surface.md:301-331`).
**If not ready:** the consumer writes an accumulator — one `var parts []specs.Specification[User]`, three lines per optional filter in this repository's own brace style, one fold. The line count is in the DX verdict; the checkable loss is that the accumulator is a statement block over a mutable slice, so the assembly can no longer be returned from a function or nested inside an `AnyOf`. Closing it is `EqPtr`/`EqOpt` on the attribute plus `If` for the genuine condition — see the DX section, which also names the widening `If` opens inside `AnyOf`.
**Before reaching for any of that, note the shipped answer this case does not use.** A filter that arrives from a request is `crud/query`'s job: the wire DSL compiles one JSON document into `crud.Options` with no per-resource assembly code at all, and it is the entry point that already caps an `in` list (`crud/query/compile.go:39-46`). This module is for filters the codebase *names* and reuses. A consumer with twenty resources who hand-writes an `AllOf` block per list endpoint has picked the more laborious of two shipped answers, and nothing in either module's docs draws the line.

### H-SPECS-03 — Rename a column and have the build break, not a customer's request
**Who:** the engineer doing a rename across a Monday afternoon
**Wants:** `go build` to name every call site.
**Story:** They rename `Article.Views` to `Article.ViewCount`, regenerate, and run the build. Their codebase has both spellings in it, because the module doc teaches the literal criteria form on the same page as the typed one.
**Must hold:**
1. Every filter that named the old field through the metamodel fails to compile.
2. A filter that names a field the model does not have fails when the process starts, not on a request.
3. Comparing a column against a value of the wrong type fails to compile.
4. After the rename and the regenerate, `go build` fails at the `SoftDelete` declaration, at the preload, at the relation scope and at the sort term — not only at the filter.
5. Adding a *new* column and forgetting to regenerate is noticed before the code that cannot see it ships.
**Today:** 🟡 partial — 1 through 4 hold; 1 holds only for the typed spelling; 5 is a test, not a refusal.
**Evidence:** `metamodel.go:206` builds the metamodel; `:252-262` checks each attribute's element type against the model and panics with the field name. The refusals are pinned in five places: `edge_test.go:214` is `TestAMetamodelThatCannotBindIsRefusedAtDeclarationTime` (non-struct, not-an-attribute, not-a-relation, wrong width, wrong type); the two relation-handle refusals are separate tests at `edge_test.go:355` and `edge_test.go:379`. Guarantee 4 is `metamodel.go:28` (`Name()`), `:161-182` (the `Rel` handle), proven at `edge_test.go:322` and driven into a real relation scope at `crud/sqlrepo/relscope_test.go:258-287`.
Guarantee 1's limit is worth stating because the docs invite it: the string form is a first-class spelling here — `specs_test.go:62-73` uses `root.Get("Active")` and `root.Get("Email")`, `crud/sqlrepo/relscope_test.go:284-285` uses `crud.Eq("Spam", false)` — and an unknown field is resolved when the statement is built, per request. Round 1 cited `crud/sqlrepo/repository.go:515` for that, which is the unknown-*sort*-field arm inside `distinctSort` and never sees a predicate; the filter path is `crud/predicate.go:86-93` (`writer.leaf` → `WalkPath` → `w.fail`) with the error built at `crud/relation.go:378`. In a codebase that mixes both spellings, a rename leaves the literal call sites compiling and returning 400s, which is the exact failure the intro promises this module prevents.
**A third [[D-021]] proviso miss, new this round, and it is guarantee 1's shape rather than guarantee 5's.** `attr:"-"` is a supported spelling and the obvious use is making a column unfilterable. `bindMetamodel` skips the field (`metamodel.go:241-244`) and leaves the attribute at its zero value, so `Name()` answers `""` — asserted at `edge_test.go:235-253`. Every method still exists (`metamodel.go:57-58` gives `Eq`/`Ne` to every `Attr`), so `User_.Password.Eq(x)` compiles, renders `crud.Eq("", x)`, and comes back as a `*crud.UnknownFieldError` → 400 (`port/kind.go:123`), per request, for as long as the code lives. The generator never emits `attr:"-"`, but the module doc's own example metamodel is hand-written (`docs/modules/en/specs.md:56-63`), so this is reachable without it.
Guarantee 5: there is no start-up check. Validation walks the *declared* attributes and asks the model about each, never the reverse (`metamodel.go:223-276`), and the only coverage assertion the generator emits is `port.MustCoverUpdate` (`_examples/example/blog/vv_gen.go:416-421`, from `internal/codegen/adapter.go:140`), which compares the model against the **update DTO** (`port/pathmap.go:175`) — so a new `generated` or read-only column, absent from the DTO by design, is absent from the metamodel with nothing to say so. What does notice is the regenerate-and-diff drift check, `TestTheGeneratedStoresAreUpToDate` in `test/codegen/codegen_test.go:38` — no build tag, no database, so it runs in `make unit`.
**If not ready:** for guarantee 1, nothing closes the mixed-spelling hole and nothing should: [[D-013]] and [[UC-007]] guarantee 18 keep the runtime-named form on purpose. For the `attr:"-"` hole, the honest answer is that a skipped attribute should refuse at bind time rather than at request time — `bindMetamodel` knows the field was skipped and could leave a sentinel name that panics on first use, or the tag could be refused on a settable field altogether. For guarantee 5 the fix is a `MustCoverAttrs[M, A]()` beside the existing `MustCoverUpdate` line — one generated line, squarely inside [[D-018]]. **Note the doc contradiction rather than the gap:** [[UC-014]] guarantee 13 claims a start-up refusal so a new column is not "quietly invisible to updates and to the typed query API", and its Status says Gap 1 closed. The check reaches updates and not the typed query API. One of the two has to move, and this sweep says the document is ahead of the code.

### H-SPECS-04 — Filter by something on the other side of a relation
**Who:** the engineer writing "articles by authors in this org" and "orders with at least one refunded line"
**Wants:** to reach one hop without hand-writing a subquery.
**Story:** They write `Article_.Author.OrgID.Eq(org)` and expect articles back — one row per article, not one per author. Then they try to sort a list by a column on the far side of a to-many edge, because the metamodel offered it.
**Must hold:**
1. A filter through a relation returns root rows, and never duplicates one because two children matched.
2. It works for to-one, to-many and many-to-many edges alike.
3. Sorting by a to-one relation's column works, and sorting by a to-many's does not compile.
4. "Has at least one child at all" is expressible.
**Today:** 🟡 partial — 1 and 2 hold; 3 holds only in its first half; 4 has no spelling of its own.
**Evidence:** a nested attribute group binds with the relation path as a prefix (`metamodel.go:265-273`) and the dotted name becomes a correlated `EXISTS`, which is [[D-005]]'s invariant — rendered at `crud/sqlrepo/relscope_test.go:74-91`. Many-to-many runs against two live engines at `test/integration/matrix_test.go:334-347`. A to-one sort is a scalar subquery (`crud/predicate.go:sortExpr`), and `crud/sqlrepo/repository.go:507-511` refuses a relation sort under `DISTINCT` rather than guessing — see H-SPECS-05 guarantee 5 for what that refusal costs a caller who never asked for it.
Guarantee 4's nearest spelling is `Order_.Lines.ID.NotNull()`, which renders the right `EXISTS` and reads as something else. The negative — "has no lines" — is `specs.Not(...)` over that, and reads worse still; a consumer who instead writes `Order_.Lines.ID.IsNull()` gets `EXISTS (… AND id IS NULL)`, which is a different question that matches nothing.
**Guarantee 3's second half is where the module's headline promise inverts.** The generator emits every target column as a full attribute inside the relation group — `_examples/example/blog/vv_gen.go:36-43` gives `ArticleCommentsAttrs` a `Body specs.Str[Article]` — and `Str` embeds `Ord` embeds `Attr`, whose `Desc()` is `metamodel.go:73-74`. So `Article_.Comments.Body.Desc()` type-checks, and is refused only when the statement is built: `crud/predicate.go:554-558` fails with a `*crud.SchemaError` reading "cannot sort through a has_many relation". That is a per-request 400 reached through the typed API, which is exactly [[D-021]]'s proviso — magic is preferred *provided it fails at build or start-up* — not being met.
Two caveats sit in the generator, three lines apart, and both are a silent `continue`: a relation whose target model is in another package is skipped (`internal/codegen/render.go:222-224`), and so is any relation past `-depth` (`render.go:217-219`, `if level+1 >= g.depth { continue }`). The second is the one that hits everybody — the default is 2, so "orders by their customer's account manager's region" is three hops and the group is absent, with the compile error naming a missing field rather than a depth setting. **Round 1 said there was no way to express it and that was wrong:** `Meta.WalkPath` has no hop limit and no package check (`crud/relation.go:356-393`), so `specs.Of` + `root.Get("Customer.Manager.Region")`, or `specs.Lift[Order](crud.Eq("Customer.Manager.Region", x))`, compiles and renders the correct nested `EXISTS` today. The module doc already advertises the crossing (`docs/modules/en/specs.md:48-50`). What the depth stop costs is the rename safety on that one call site, not the query.
Guarantee 4's twin defect — what *two* conditions on one to-many relation mean together — is H-SPECS-22, and it is sharper than either of these.
**If not ready:** "has any" is written as a not-null test on the child's key, or as `specs.Lift` over a hand-built predicate. See the DX section for the cheap shape — `Rel` already knows its target model (`metamodel.go:281-291`) and `crud.Schema` exposes `PK` (`crud/meta.go:94`), so no new AST node is needed. The to-many sort has exactly one fix and round 1 offered two: only the **generator** can withhold `Desc()`, by emitting a sort-less attribute type inside a to-many group. The alternative round 1 named — "`Rel` carries the edge kind so the sort methods can be withheld" — is not implementable in Go: `Desc()` is promoted onto `Attr[M, T]` (`metamodel.go:73-74`) and the handle is a *sibling field* of that attribute in the generated group (`vv_gen.go:36-43`); a sibling cannot remove a promoted method from a different type. So [[D-018]] and `cmd/vv` own the whole of that blocker rather than sharing it with `specs`.
Guarantee 4's rename story is H-SPECS-03's guarantee 4 and is not restated here.

### H-SPECS-05 — Sort and page a filtered admin table, with a total
**Who:** the engineer wiring a data grid
**Wants:** page 3 of the filtered rows, sorted, plus the total so the grid can draw its pager.
**Story:** They call the paged variant with the filter, a page, a limit and a sort term taken from the metamodel. The sort column is nullable, so they ask for nulls last. They deploy to MySQL. Later the same screen grows a "unique rows only" toggle.
**Must hold:**
1. The filter, the sort and the paging combine in one call, and the caller's own options are not overwritten.
2. The `COUNT` carries the specification, so the total counts the filtered set.
3. The sort term is written in identifiers, like the filter.
4. Nulls-last sorts nulls last on every supported engine, or the call site says which engines it does not.
5. The sort term works alongside the other read options the same screen sends, a distinct projection among them — and where it cannot, the refusal is one the caller could have avoided.
**Today:** 🟡 partial — 1, 3 and 4 hold; 2 is ❓ unverified *through a specification*; 5 fails for a declaration this module encourages.
**Evidence:** guarantee 1 is `executor.go:68` — `FindPage` prepends `As(s)` and passes the caller's options through untouched. Guarantee 3 is `metamodel.go:73-74`, run live at `test/integration/suite.go:560-566`.
Guarantee 2 is where round 1 was wrong, and the correction matters: `test/integration/suite.go:470-476` calls `FindPage(ctx, User_.Name.Contains("user"), …)` against ten seeded rows and asserts `page.Total != 10`. Every seeded row matches — `suite.go:495` asserts `User_.Name.StartsWith("user")` matches all ten — so **the assertion passes unchanged if the specification is dropped from the `COUNT` entirely.** The property does hold structurally, since `FindPage` builds one `Options` that both statements are built from, and the equivalent is pinned for the string form at `crud/sqlrepo/repository_test.go:124`; but that goes through `crud.Where`, not through a specification, and nothing pins this one.
Guarantee 4 was graded a leak in round 1 and the code reviewer is right that it is not. The guarantee's own escape clause is "or the call site says which engines it does not", and `WithNullsLast` **is** the call site — `Desc()` returns a `crud.Order` and that method is the only way to ask for nulls-last. Its godoc says which engine ignores it and what that engine does instead (`crud/predicate.go:524-527`). The behaviour is [[D-019]] difference 1, pinned at `crud/predicate_test.go:265` and run live at `test/integration/dialect_edge_test.go:486-520`. Round 1's proposed fix also named methods that do not exist: `Asc`/`Desc` are declared on `Attr` and promoted, not on `Str`/`Ord`.
**Guarantee 5 is new this round and it is a 400 the client cannot avoid.** `distinctSort` refuses any sort whose field contains a dot, unconditionally and *before* the `asked` test (`crud/sqlrepo/repository.go:506-512`). The comment eight lines above it (`:494-496`) states the intended rule: a caller's own sort is refused because the two requests genuinely conflict, while the repository's default sort — "which the caller never asked for" — is dropped rather than turned into "an error they cannot avoid". A relation sort never reaches that test. This module supplies both halves of the collision: `sqlrepo.DefaultSort(Article_.Author.Name.Asc())` is the metamodel-addressed declaration H-SPECS-03 celebrates, and `?distinct=1` is an ordinary wire option. Together they are a `*crud.SchemaError` on a request the client did nothing wrong in sending.
**If not ready:** for guarantee 2, one recorder test in `specs_test.go` asserting the `COUNT`'s `WHERE` carries the specification — six lines using the `where` helper's shape. Change the integration filter to one that matches some rows and not all of them while you are there; the current one cannot fail. Guarantee 5 is `sqlrepo`'s line to move — the dot check belongs inside the `asked` arm — but it is reachable only through a metamodel sort term, so the reader who needs the warning is here.

### H-SPECS-06 — Look one row up by a business key
**Who:** anyone writing "find the user with this email"
**Wants:** the row, a clean not-found, and a loud complaint if the "unique" column turns out not to be.
**Story:** They call the find-one variant with an equality on email. Later, a bad migration drops the unique index and two rows share an address.
**Must hold:**
1. No match is a not-found error and the returned model is the zero value.
2. Two matches is a distinct error, not the first row silently.
3. That error is the one a transport already maps to 409.
4. A caller who also passes paging cannot turn the uniqueness assertion off.
5. "Any of them will do" is a different, named call.
**Today:** ✅ ready
**Evidence:** `executor.go:31-47` fetches two rows and branches on the count; `executor.go:107-111` (`take`) overwrites the caller's page, offset, limit and unpaged flag rather than merging them, with the comment naming the bug that caused it. `errors.go:13` wraps `crud.ErrConflict`, which is [[D-015]]'s table. `edge_test.go:123-208` is the whole contract: two matches conflict, the zero model comes back, four different caller paging options each fail to disarm the `LIMIT 2`, the sentinel still wraps the conflict, and find-first takes the first with `LIMIT 1`. This is the best-tested corner of the package and the one place a wrapping decision was honoured without being asked.
**If not ready:** n/a.

### H-SPECS-07 — Deactivate or purge everything a filter selects
**Who:** the engineer writing the 90-day inactivity sweep and the tenant-offboarding job
**Wants:** one statement, and a guard rail against the version of it that empties the table.
**Story:** They call the update-by variant with a filter and a DTO. A month later somebody refactors the filter builder and it starts returning an empty filter under a condition nobody tested — the upstream id list came back empty and the filter was a `NotIn` over it. A month after that, the DTO builder regresses and sets nothing.
**Must hold:**
1. It is one statement, not a select-then-loop.
2. A filter that selects nothing in particular refuses rather than matching every row, and issues no statement.
3. That refusal is distinguishable from a database error by the caller and by a transport.
4. "Really do mean every row" has a different name that says so.
5. On a soft-deleting repository the consumer can tell from the API which statement they are about to send.
6. A payload that writes nothing is as loud as a filter that selects everything, and the caller knows which bookkeeping columns a bulk write touches.
**Today:** ❌ 2 fails for the shape that actually occurs; 3 fails; 6 fails; 1 and 4 hold; 5 is undocumented.
**Evidence:** `executor.go:83-101` compiles the specification once and refuses when it is nil. `edge_test.go:77` (`TestDeleteByRefusesEveryShapeOfEmptySpecification`) runs that refusal across all thirteen spellings and asserts nothing reached the database — **but it is delete-only.** `grep -rn 'UpdateBy(' --include='*.go' .` returns `executor.go:93`, `test/integration/suite.go:569` and `:580`, and `:580` uses the single spelling `specs.Where[User](nil)` behind the database-backed suite. This case's own story is update-by and the coverage behind it is one spelling; [[UC-007]]'s Status already records it.
**The guard tests `p == nil` and nothing else, and a tautology is not nil.** `crud.NotInAny` over an empty slice renders `1 = 1` (`crud/predicate.go:201-209`), and so does `cb.Conjunction()` (`crud/predicate.go:349-357`). `DeleteBy(ctx, User_.ID.NotIn(stillActive...))` with an empty `stillActive` sends `DELETE FROM "users" WHERE 1 = 1`. **The second guard has the same blind spot.** `crud/decorators/security/security.go:724` refuses an unscoped `DeleteAll` with `if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedDeleteAll` — also a nil test, over an option set that now carries a non-nil `1 = 1`. Two independent layers exist to stop this and the same input walks through both. The read half of the same node is H-SPECS-20.
For guarantee 3, `errors.go:16` and `:19` wrap no core sentinel at all, so `port.KindOf` falls through `sentinelKind` (`port/kind.go:110-128`) to `errs.KindInternal` and a refused bulk write reaches the client as 500.
Guarantee 5: `DeleteAll` becomes an `UPDATE` stamp when the blueprint declares `SoftDelete` (`crud/sqlrepo/repository.go:903-907`, `stamp` at `:933-941`), so `DeleteBy` on a soft-deleting model is a tombstone write, not a purge — and the `ErrUnboundedDelete` guard reads very differently once the worst case is a stamp rather than a truncate. Neither `docs/modules/en/specs.md` nor the method doc says the two meet. The read half of soft delete is H-SPECS-18.
**Guarantee 6 is new this round.** `UpdateAll` returns `(0, nil)` when the write plan is empty (`crud/sqlrepo/repository.go:836-844`), with a comment defending it, and the decision is defensible on its own. What makes it a pair with guarantee 2 is the asymmetry: the sibling failure gets a sentinel and thirteen spellings of test, and this one gets a zero. The offboarding job whose DTO builder regressed logs the same line as the offboarding job whose filter matched nothing — "0 rows updated" — and the on-call reads both as "nothing matched". The second half of guarantee 6 is one sentence that exists nowhere: a bulk update **bumps the version column on every row it touches** (`:857-859`, with the comment explaining that a stale `Update` would otherwise sail past it) and stamps no updated-at. A consumer with optimistic locking running a bulk write against rows other people are editing needs that fact before the run, not after.
**If not ready:** the consumer checks the emptiness of their own id slice before calling, which is exactly the check they came here to stop writing. Closing guarantee 2 needs `crud` to answer "is this predicate unconditionally true", and **that answer cannot be written in `specs` or in `security` without challenging [[D-054]]** — see "What it must not break", which names the decision and the two shapes. Guarantee 3 is not a `%w` on an existing sentinel: **there is no `crud.ErrBadRequest`** (`crud/errors.go` declares seven sentinels and none is it; the one this needs is `port.ErrBadRequest`, `port/sentinel.go:14`), and the cheapest correct shape is also in "What it must not break". Guarantee 6 is a doc sentence on `UpdateBy` plus, if the owner wants symmetry, a sentinel for the empty plan — though that is a `sqlrepo` contract change and a `(0, nil)` that has always been a success.

### H-SPECS-08 — The case-insensitive search box
**Who:** every consumer, in week one
**Wants:** "type a fragment of a name, get the matching rows, don't care about case".
**Story:** They put a text input above the admin list. Someone searches for `ann`. Someone else searches for `50%`. The team runs SQLite in tests and PostgreSQL in production. Half the team copied the literal criteria form from the top of the module doc and half copied the metamodel form from below it.
**Must hold:**
1. A user's `%`, `_` or backslash is matched literally, not as a wildcard, on every dialect the library supports and through every spelling the module doc teaches.
2. Matching ignores case, or the call site says what decides it.
3. Both at once, in one call.
**Today:** ❌ 1 is false on SQLite through the typed form and false on every dialect through the literal form; 3 is missing; 2 is a filed dialect difference with nothing at the call site.
**Evidence.** `Contains`/`StartsWith`/`EndsWith` (`crud/predicate.go:436-438`) escape with `escapeLike` (`:490-493`, a three-line `strings.NewReplacer` producing `\\`, `\%`, `\_`) and the pattern travels as a bind argument. **But `likeNode.render` (`crud/predicate.go:255-275`) emits `col LIKE ?` and nothing else — `grep -rn ESCAPE --include='*.go' .` returns only `crud/catalog/sqlite.go`, which writes `ESCAPE '\'` by hand.** PostgreSQL and MySQL default `LIKE`'s escape character to backslash, so the escaping works there by luck of the default. **SQLite has no default escape character**, so on SQLite the pattern `%50\%\_off%` is read as literal-backslash plus wildcard, and `Contains("50%")` silently returns zero rows. SQLite is a supported dialect: `crud/dialect.go:158-160`, `crud/adapter/crudsql/crudsql.go:162`, `crudtest.New(crud.SQLite{})`.
**The literal form has no escaped operator at all, and that is wider than the SQLite hole.** `specs.Builder` offers `Like`, `NotLike` and `LikeIgnoreCase` and nothing else (`spec.go:78-82`; the same list printed at `docs/modules/en/specs.md:166-172`). The module doc opens with that form (`:33-46`) and `specs_test.go:69-73` uses it. So `cb.Like(root.Get("Name"), "%"+q+"%")` ships a search box where a user's `%` is a wildcard on **PostgreSQL, MySQL and SQLite alike**, and `escapeLike` is unexported, so it cannot be fixed at the call site. This re-prices the fix list: exporting `crud.EscapeLike` and giving `Builder` the three escaping operators is not the second-cheapest item, it is the one that covers the form the docs teach first.
**Round 1's account of why the tests miss it was wrong on both halves, and both halves change the work.** Two of the three cited tests pin the clause exactly, so emitting `ESCAPE` breaks them and the owner needs that in the estimate: `crud/predicate_test.go:55-65` is a `{name, predicate, wantSQL, wantArgs}` table asserting `"title" LIKE $1` as well as the argument, and `crud/sqlrepo/repository_test.go:552-555` asserts the full clause including `"email" LIKE $7`. Only `crud/query/hostile_test.go:199` is loose. And a **live-row test of exactly this property already exists**: `test/integration/edge_test.go:748-776`, "LIKE metacharacters in a value are literal", runs `Contains("Name","%_")`, `StartsWith("Name","100%")` and `EndsWith("Name","_raw")` against seeded rows and asserts one match. It misses SQLite only because it loops over `egEngines()` (`test/integration/edge_test.go:361-368` = postgres, mysql, mariadb) while SQLite runs `RunSuite` alone (`test/integration/driver_sqlite_test.go:40-41`). The regression test belongs in that loop, not in a new file.
This makes [[UC-007]] guarantee 9 — "pattern-matching convenience operators escape the wildcard characters in their argument, so a value containing one is matched literally" — false on one of three engines, and it makes [[D-019]]'s invariant false for an unnamed twelfth difference. **[[UC-007]]'s Status and its Index row have to move with it**: the Status reads "covered, with three caveats" (`docs/ai/usecases/modules/specs/UC-007-…:91`) and never mentions guarantee 9, and `docs/ai/usecases/Index.md:78` carries UC-007 as `covered`. An owner scanning the Index at tag time sees green for the one use case this sweep says is red.
Guarantee 2: `Contains` renders no `LOWER()` and no `COLLATE`, so the column's collation decides — MySQL's default is case-insensitive and PostgreSQL's is not. That is **already filed** as [[D-019]] difference 4, which names `LikeIgnoreCase` as the portable spelling. Guarantee 3 is the hole that leaves: `LikeIgnoreCase` (`crud/predicate.go:430-434`, `metamodel.go:108-110`) folds both sides with `LOWER()` and passes the pattern straight through unescaped. The portable spelling and the escaping spelling are disjoint.
**If not ready:** the consumer writes `User_.Name.LikeIgnoreCase("%" + q + "%")` and ships a search box where `50%` matches every row. Four fixes, and the first is the release blocker:
1. **Emit `ESCAPE` unconditionally from `likeNode.render`, dialect-aware.** Not "whenever the pattern came from an escaping constructor" — that was round 1's proposal and it breaks [[D-054]]; see "What it must not break". Unconditional is a no-op on PostgreSQL and MySQL, whose default escape character is already backslash, so behaviour changes only where it is currently wrong. It is not a three-character append: MySQL treats backslash as an escape inside string literals, so the clause has to be written `ESCAPE '\\'` there while SQLite and PostgreSQL take `ESCAPE '\'`. The seam already exists — the nulls-ordering arm branches on `w.d.Name()` at `crud/predicate.go:597`. Scope it as a writer change with a per-dialect literal, update the two pinned clauses (`crud/predicate_test.go:55-65`, `crud/sqlrepo/repository_test.go:552-555`), and add a SQLite target to `egEngines()` so the live test that already asks this question asks it on the engine that gets it wrong.
2. **Export `crud.EscapeLike`** so a hand-built `Like` pattern can be made safe once for all three operators.
3. **Give `specs.Builder` `Contains`/`StartsWith`/`EndsWith`** and their ignore-case siblings, in the same change. The criteria form cannot express the escaped operators at all today, so the form the module doc presents as equivalent is silently the less safe of the two.
4. **Add `ContainsIgnoreCase`/`StartsWithIgnoreCase`/`EndsWithIgnoreCase`** pairing the existing escape with the existing fold. This needs **no new AST node and no wire change** — it is `likeNode{ignoreCase: true}` over an already-escaped pattern, which marshals through the existing `ilike` operator (`crud/document.go:199-217`) and compiles back through `opILike` (`crud/query/filter.go:275-290`). A new *JSON operator name* would be a wire-contract addition with its own costs (the fuzz seed corpus, a round-trip test, the operator table in the query module doc, and an older server answering [[D-013]]'s rejection to a client that sends it) — do not price them as one job.
Repeat [[D-019]]'s own note in the constructor doc while you are there: `LOWER(col) LIKE LOWER($1)` is served by no plain B-tree index, so PostgreSQL needs a `lower(col)` expression index and MySQL a `_ci` collation. D-019 already records it as the ILIKE row's cost; the constructor is where a consumer reads it.

### H-SPECS-09 — Filter by a list of ids from another service
**Who:** the engineer joining across a service boundary — "load the orders for the customer ids this other API returned"
**Wants:** an `in` over a typed slice, with the list as large as the upstream page happens to be.
**Story:** The upstream returns ninety thousand customer ids for a large tenant. The filter is built and run. It worked in the test that used three.
**Must hold:**
1. `in` over a typed slice takes the slice, not a hand-rolled `[]any`.
2. A list too large for the driver is refused as a caller error, not built and sent.
3. Whatever the ceiling is, the consumer can find it before production does.
**Today:** 🟡 partial — 1 holds; 2 and 3 have nothing.
**Evidence:** guarantee 1 is `metamodel.go:64-70`, which takes `...T` and routes through `crud.InAny` (`crud/predicate.go:453-455`). For 2 and 3 there is nothing: `inNode.render` (`crud/predicate.go:211`) binds one parameter per element, PostgreSQL refuses a statement past 65535 of them, and the failure arrives as a driver error — a 500, after the statement was built.
**The library caps the other two entry points for exactly this reason, and this one is the exception.** `crud/query` defaults `MaxInValues` to 1024 (`crud/query/compile.go:39-46`) with the comment: "PostgreSQL refuses a statement past 65535 of them, so without this the honest 400 arrives from the driver, as a 500, after the statement was built." `crud/preload.go:16-17` chunks parent keys at 900 (`preloadBatch`), applied at `:221`. So a wire-driven `in` is capped, a preload's `in` is chunked, and `User_.ID.In(ids...)` — the one a consumer reaches for first — is neither.
Round 1's guarantee 2 said "refused **or chunked**", and chunking is not implementable here: a preload can chunk because it is its own statement, while an `IN` inside a composed `WHERE` tree cannot be split without running N statements over an arbitrary predicate. The word is dropped.
**If not ready:** the consumer chunks the slice themselves and ORs the pieces, or finds out on the day the upstream returns a big page. The cheap half is guarantee 3: state the ceiling on `Attr.In`/`NotIn` and in `docs/modules/en/specs.md`, and point at `crud/query`'s cap as the number to copy. The real fix belongs in `crud`, beside `inNode`, because the Go path and the wire path should not disagree about what is too many — and **the refusal has to be a `*crud.SchemaError`**, which `port/kind.go:123` already maps to `KindBadRequest` through an `errors.As` arm. A plain error here would reach the client as 500, which is the defect this fix exists to remove.

### H-SPECS-10 — Filter on a nullable column, a soft-delete column, and a domain type
**Who:** the engineer with `DeletedAt *time.Time`, `Rating crud.Opt[float64]` and `Total Cents` where `type Cents int64`
**Wants:** to say "not deleted", "rated above 4", "between 1000 and 5000 cents", and to point the soft-delete declaration at the same column without a literal.
**Story:** They generate the metamodel and expect the attribute types to be about the *value*, not about the wrapper. Then they try a range filter on the money column.
**Must hold:**
1. A nullable column's attribute compares against the plain value type, not a pointer and not an optional.
2. Null and not-null are available on every attribute.
3. A column whose Go type is a named type over a builtin gets the operators that builtin would have got.
**Today:** 🟡 partial — 1 and 2 hold; 3 does not, and the limit is the generator's rather than the library's.
**Evidence:** `crud/meta.go:521-530` (`ElemType`) strips both the `Opt` wrapper and the pointer, and `metamodel.go:257` compares against that. The model is `_examples/example/blog/model.go:46` `Rating *float64` and `:47` `PublishedAt crud.Opt[time.Time]`; the generated **attributes** are `vv_gen.go:59-60` (`Rating specs.Ord[Article, float64]`, `PublishedAt specs.Cmp[Article, time.Time]`). `IsNull`/`NotNull` are on the base attribute (`metamodel.go:59-62`), run live at `test/integration/suite.go:501-502`.
Guarantee 3 fails at `internal/codegen/codegen.go:354-374`: `attrType` matches the **literal type string** against `ordered`, a map of twelve builtins, plus `string` and `time.Time`. Everything else falls to `specs.Attr[M, T]`, which has no `Gte`/`Lte`/`Between` and no `Contains`/`Like`. So `type Cents int64` gets equality and null tests and no range; `type Status string` gets no pattern operators; `decimal.Decimal` and `civil.Date` get neither. Hand-editing `vv_gen.go` is lost at the next `make generate`.
**It is a generator choice and not a library limitation**, which is what makes it cheap: `specs.Ord[Order, Cents]` compiles and binds today, because the validation compares the *reflect* type (`metamodel.go:257` against `crud/meta.go:520-530`), not the source text.
**If not ready:** round 1 priced the workaround too high and the dx reviewer is right. `Name()` is declared on the embedded base `attr` (`metamodel.go:28`), so every attribute type has it, including the `specs.Attr[Order, Cents]` the generator emits. The workaround is `specs.Lift[Order](crud.Gte(Order_.Total.Name(), low))`, which **keeps** the rename safety — the string is bound at package init. What is lost is the compile-time value-type check, because `crud.Gte(field string, value any)` takes `any` (`crud/predicate.go:418`), so `Gte(Order_.Total.Name(), "cheap")` compiles and fails per request. That is a smaller cost than round 1 claimed and it is still the wrong side of [[D-021]]'s proviso.
Closing it needs no flag for the common case: `attrType` switches on the source-text spelling, so resolving a same-package `type Cents int64` to its underlying kind is a generator change alone. Say plainly that this reaches same-package declarations only — the generator reads source text, not types ([[D-050]]) — so an imported `decimal.Decimal` still needs an explicit escape, and **that** is what a flag is for. Do not spell the flag `-types`: that name is taken and means the comma-separated list of model names (`cmd/vv/main.go:20`, `:67`). `-attr` or `-ordered` says what it does.
Whether a column *can* be null stays out of the type deliberately — [[UC-007]] scopes that out. What it does to a *negation* is H-SPECS-17, and that is not scoped out anywhere.

### H-SPECS-11 — Test the filter without a database, and see it again when it misbehaves
**Who:** the engineer who put a pricing rule in a filter and wants it in the unit suite — and the same engineer, at 2am, when that filter returns nothing
**Wants:** a table test that asserts what the filter means, and a way to see what it compiled to in production.
**Story:** They have `eligibleForRefund()` and eight cases. They want them to run in `make unit` with the containers stopped. Three months later the filter returns zero rows against production data and three teams share it.
**Must hold:**
1. The filter tests run with no database up.
2. There is a supported, documented way to assert what the filter says, in a few lines.
3. The assertion fails when the filter changes meaning.
4. When a named filter returns nothing in production, somebody can see the `WHERE` it produced.
**Today:** 🟡 partial — 1 and 3 hold; 2 exists and is not signposted; 4 has no answer inside the library.
**Evidence:** guarantee 1 holds by construction — a specification is a pure function of `(root, builder)` (`spec.go:12-13`), and the package's own tests need no database. Guarantee 2 is where round 1 overstated the gap: **`crudtest` is built for exactly this job**, with its own use case. Round 1 called it "a published module" and it is not — `find . -name go.mod` returns thirteen files and none is under `crud/crudtest`; `docs/modules/en/Index.md:141` lists it in the contract manifest, not as a module. That makes the situation better than described: a consumer who imports `crud` already has it, with no extra `require` line. Its whole exported surface is `Normalize`, `Recorder`, `MySQL`, `New`, `Postgres`, `Result`, `Rows`, `RowsFailing`, `Statement` (`docs/api/surface.md:254-263`), and the six-line helper at `specs_test.go:51-60` uses nothing else — `crudtest.Postgres().Push(crudtest.Rows())`, then `rec.Last().SQL`. It is documented under a title that is literally this job (`docs/modules/en/crudtest.md:1`) and tied to [[UC-011]]. The defect is one missing cross-link: `docs/modules/en/specs.md:214-219`'s "See also" names `cmd/vv`, `crud`, [[UC-007]] and [[D-018]] — and not `crudtest`.
The second route is `crud.MarshalPredicate(specs.Predicate(sp))` (`crud/document.go:31`), two lines, producing an assertable JSON document — but by [[D-054]] it refuses `crud.Raw`, `crud.EqField` and `crud.False` by name, so it cannot assert a filter built on the escape ladder's third rung.
Guarantee 3 holds against the SQL text only. There is no way to ask "does this model satisfy this specification" in memory, so what a consumer tests is the shape of the clause, not which rows it selects.
**Guarantee 4 is new this round, and the answer is that the library does not help.** There is no logging seam inside the repository — `grep -rn 'port.Logger' crud/sqlrepo/*.go` is empty, and every `port.Logger` call site in the tree is in a transport. So the two answers are the two above, pointed at a different moment: rebuild the specification in a test with `crudtest`, or marshal it and read the document, which is blind to a `Raw` rung. Neither is offered for this job, and the expectation that this library does not log statements for you is worth setting before the incident rather than during it.
**If not ready:** one paragraph in `docs/modules/en/specs.md` showing `crudtest.Postgres()` + `rec.Last().SQL`, a "See also" row, and one sentence saying that the same two routes are what you have when the filter misbehaves in production. That is the whole fix, and pricing it as a missing package cost round 1 a serious-sounding row it did not deserve.

### H-SPECS-12 — Use the same filter everywhere else
**Who:** the engineer who has one `visibleToTenant()` and four places that need it
**Wants:** the same value to work against a plain repository call, a relation scope, an HTTP handler's per-request narrowing, and a call to another service.
**Story:** They pass it to the repository they already had, to a preload declaration, into the handler option that narrows every route, and to the remote client. Their conclusion is that the tenant filter now protects the resource.
**Must hold:**
1. A filter is convertible to a plain query option in one call, and to a bare predicate for the declarations that take one.
2. The repository type already named in their service interfaces does not have to change when they start using filters.
3. A filter ANDed onto a repository that was already narrowed cannot widen it.
4. Where a destination does *not* carry the filter — or carries it on reads only — the call site says so.
**Today:** 🟡 partial — 1, 2 and 3 hold; 4 fails on the remote leg, and the handler leg is a handoff.
**Evidence:** guarantee 1 is `spec.go:196` (`Predicate`) and `:204` (`As`); `edge_test.go:61` asserts an empty specification through `As` adds no clause at all rather than an empty one. Guarantee 2 is `executor.go:24` — `Executor` takes the `crud.Repo` the consumer already holds and returns a struct embedding it, so `GetByID`/`Save`/`Update`/`Tx` all still resolve. Guarantee 3 is `edge_test.go:257` (`TestASpecificationCannotEscapeARepositoryScope`), which asserts the rendered SQL *and* the argument order, so it proves which predicate came first — [[D-004]] holding through the decorator. The relation-scope route is `crud/sqlrepo/relscope_test.go:274-287`, with the literal spelling beside it as the control.
**Guarantee 4, the remote leg — not evidenced and does not fully work.** `remote/options.go:90-96` routes every predicate through `crud.MarshalPredicate` and returns its error, and [[D-054]] refuses three nodes by name (`crud/document.go:221`, `:297`, `:301`). So a specification built on `cb.Raw` — the third rung of the ladder the DX section below calls this design's best property — cannot cross a process boundary at all. The intro promises "a service in another process"; the ladder breaks that promise two pages later.
**The handler leg is `crudnet`'s contract and is handed off, with a pointer kept here because a specs consumer is who draws the wrong conclusion.** `crud/http/crudnet/options.go:97` takes `[]crud.Option`, so `specs.As` drops in, and the seven lines of comment above it (`:90-96`) say the scope is reads only: "with a scope of `TenantID = 7`, `GET /{id}` on somebody else's row is 404 while `DELETE /{id}` on the same row answers 200. Row-level rules on writes belong in `security.Gate`." The option, the comment and the fix are all `crudnet`'s and `security`'s; the finding belongs in the crudhttp/crudnet sweep and is not scored as a specs gap here.
**If not ready:** for the handler leg, one sentence in `docs/modules/en/specs.md` and in `As`'s own doc comment: row-level rules on writes are `security.Gate`'s, and a specification passed to `DeleteBy`/`UpdateBy` reaches it (see H-SPECS-14). For the remote leg, `remote`'s error is honest and arrives at the call site; what is missing is a line in the module doc listing the three refusals, so a consumer chooses the rung knowing which door it closes.

### H-SPECS-13 — Purge a tenant and write the audit row in one transaction
**Who:** the engineer writing the offboarding job
**Wants:** the bulk delete, the audit insert, and one commit or one rollback.
**Story:** They open a transaction, call the delete-by variant inside it with the tenant filter, insert an audit row through a second repository, and commit. Later they add a read inside the same transaction and expect it to see the transaction's own writes.
**Must hold:**
1. A specification query inside a transaction joins the transaction already in the context — no rebuilt executor, no second repository.
2. It spans repositories, so the audit row and the purge commit or roll back together.
3. A read inside the transaction sees the transaction's own writes.
**Today:** ❓ unverified — the behaviour is right by reading and **nothing tests it**, and it is undocumented here besides.
**Round 1 graded this ✅ and that was inconsistent**, as both reviewers said: H-SPECS-05 guarantee 2 was downgraded to ❓ on exactly this footing two cases earlier. The rule stated at the top of this section is applied here instead.
**Evidence:** `crud.InTx` (`crud/executor.go:503-528`) takes the datasource and joins through the context: `if _, ok := ExecutorFor(ctx, src); ok { return fn(ctx) }`, otherwise `BeginnerOf(src)` and `push(ctx, …, tx, true)`. Every `specs.Repo` method passes `ctx` straight to the embedded `crud.Repo`, which passes it to `Core`, so `DeleteBy`, `UpdateBy`, `FindAll` and `CountBy` all land on the transaction's executor with no wiring change. `Core.Tx` (`crud/repo.go:53` — round 1 said `:51`, which is `Exists`) is promoted through `crud.Repo` and then through `specs.Repo`, so `users.Tx(ctx, fn)` works on the executor directly. Guarantee 2 is `InTx`'s own doc example (`crud/executor.go:499-502`), spanning two repositories. Guarantee 3 is addressed by none of that evidence.
`grep -rn 'InTx\|\.Tx(' --include='*_test.go' crud/decorators/specs/ test/integration/` returns no file that puts a specification inside a transaction; `test/integration/suite.go:426` builds the executor and `:591` builds the gated repo in separate subtests, with no transaction around either.
**Most of this is `crud`'s case, not this module's.** Every line of the evidence is `crud.InTx`, `crud.Repo` and `Core.Tx`, and the transaction gaps are already carried against [[UC-005]] as items 7 and 8 in `docs/ai/usecases/Index.md`. The specs-shaped residue is exactly two things: `docs/modules/en/specs.md` never mentions transactions, and no test walks a specification through one.
**If not ready:** three lines in the module doc, and one test that runs `DeleteBy` inside `Tx` and asserts a rollback left the rows — the same shape as the existing empty-filter test, with the recorder replaced by the live suite. That test also settles guarantee 3, which nothing addresses today.

### H-SPECS-14 — A bulk delete behind a filter still obeys the access-control policy
**Who:** the engineer who wired a per-tenant gate and then wrote the offboarding job
**Wants:** the row-level scope to reach `DeleteBy` and `UpdateBy`, not only the per-id calls.
**Story:** They wrap the repository with the gate, wrap that with the executor, and call the delete-by variant. `DeleteBy` and `UpdateBy` skip the per-id path where a gate does most of its work.
**Must hold:**
1. A specification bulk write passes through the gate and inherits its scope.
2. The order is forced rather than chosen, so a consumer cannot wire the executor underneath the gate by mistake.
3. The gate's own refusal of an unscoped bulk delete still fires.
**Today:** ❓ — 1 and 2 are right by reading and **nothing tests any of it**; 3 fails for the same input as H-SPECS-07 guarantee 2.
**Evidence:** guarantee 1: `DeleteBy` calls `r.DeleteAll(ctx, crud.Where(p))` (`executor.go:88`) and `UpdateBy` calls `r.UpdateAll(ctx, dto, crud.Where(p))` (`executor.go:98`). `crud.Repo` shadows only `Update` and `UpdateAll` (`crud/repo.go:80`, `:96`), and both forward to `r.Core`, so both land on the decorator chain and reach `gate.DeleteAll` (`crud/decorators/security/security.go:716`) and `gate.UpdateAll` (`:641`), which narrow with `g.scoped(ctx, opts)`.
Guarantee 2 is forced by the types: `specs.Executor` takes a `crud.Repo` and returns a `specs.Repo`, which is not a `crud.Repo`, so a middleware — which is `Middleware[M, ID] = func(Core) Core` (`crud/repo.go:58`) — can only ever be applied beneath it. Nothing has to be remembered.
Guarantee 3 is `security.go:724`, and it is the second half of blocker 1 rather than a finding of its own: see H-SPECS-07. It is recorded here only so a reader of this case does not conclude the gate closes what the executor leaves open.
**Nothing in the tree wires the two together**: `grep -rln 'specs\.' --include='*_test.go' .` lists no file under `crud/decorators/security/`, and the integration suite's `SecurityGate` subtest (`test/integration/suite.go:589-660`) contains no `specs.` call at all. "The offboarding job deleted another tenant's rows" is the worst outcome this module can produce, and it is one test away from being pinned.
**If not ready:** the behaviour is right; the proof is absent. One test in `test/integration` asserting `DeleteBy` under a gate with a `TenantID` scope removes the caller's rows and not the other tenant's, with the control the house rule asks for — the same call with the gate removed, asserting the leak *is* there. That control is what would notice if the wrapping order ever became a choice.

### H-SPECS-15 — Filter articles by their comments, and load the comments too
**Who:** the engineer building an article feed
**Wants:** "articles that have an approved comment, with their comments loaded" — one sentence to them.
**Story:** They write `Article_.Comments.Approved.Eq(true)` for the filter and add a preload of `Comments`. The list comes back with the right articles and every comment on them, approved or not. In review both spellings look right.
**Must hold:**
1. Filtering by a relation and narrowing the loaded children are visibly different calls, so a consumer cannot pick one meaning the other.
2. The difference is discoverable from the filter's own call site, not only from the page that happens to explain it.
**Today:** 🟡 partial — 1 holds and is documented; 2 fails.
**Evidence:** the two mechanisms are unrelated by design and both are correct. The filter is a correlated `EXISTS` over articles ([[D-005]]); the child narrowing is `sqlrepo.RelationScope` for a declaration and `crud.PreloadWhere` for a request, and `RelationScope`'s doc comment says exactly why a scope does not reach there — "a preload is a second statement against a second table and a nested filter opens a correlated subquery with its own `FROM`, so neither inherits the parent statement's `WHERE`: without this, `?preload=comments` hands back exactly the rows the article's own scope exists to hide" (`crud/sqlrepo/blueprint.go:115-127`). `docs/modules/en/specs.md:137-150` gives the trap its own section with the "Not `Article_.Comments.Approved`" warning, which is the tell that it is what consumers get wrong. The mechanism itself belongs to the preload/sqlrepo sweep and is not scored here.
**Round 1 said guarantee 1 "holds only for a reader who found that section" and the code reviewer is right that this over-claims.** The trap section sits at `:137-150`, *above* Composing at `:152`, and the same example appears above both at `:79`. A linear reader meets the warning before the composing examples. What is actually missing is narrower: the generated relation attributes carry no doc comment saying which model their predicate is about, so the reader who arrives from a completion popup rather than from the page has nothing. JPA's fetch join sets exactly the opposite expectation for the audience this module is written for.
The other direction — handing a specification for the *wrong* model to a preload narrowing — is H-SPECS-21, and it has no compile-time guard at all.
**If not ready:** the consumer ships a UI showing a row beside children the filter implies were excluded — a wrong answer with no error anywhere. The fix is a doc comment on the relation group's attributes naming the model the predicate is about, plus the cross-reference; the mechanism is already there and already right.

### H-SPECS-16 — Port a Spring Data repository layer and find out early what does not come across
**Who:** the engineer who has written `JpaSpecificationExecutor` before
**Wants:** to know within an hour which of their habits have no equivalent, rather than one method at a time.
**Story:** They map `findAll(spec, pageable)`, `count(spec)`, `delete(spec)` onto the executor and get a long way. Then they reach for a projection, a fetch join, a `Slice`, an `@Query` and a `Sort` carried on the `Pageable`.
**Must hold:**
1. The verbs that do map are named recognisably, and the ones renamed to avoid a collision say why.
2. The habits with no equivalent are listed in one place.
3. Precedence in the fluent chain matches what a JPA reader expects, or the difference is written down.
**Today:** 🟡 partial — 1 holds; 2 has nothing; 3 is undocumented and is a silent widening.
**Evidence:** guarantee 1 is `executor.go:9-20`, whose doc comment names the three collisions and their `By` suffixes — `CountBy`, `ExistsBy`, `DeleteBy`. Guarantee 2: nothing anywhere lists what does not port. Projections and grouping are out of scope by [[UC-007]]; a fetch join is H-SPECS-15's two mechanisms; `Slice` versus `Page` is `crud.SkipTotal()` (`crud/options.go:184`) or `crud.After`, which also skips the `COUNT` — **round 1 wrote `crud.NoTotal`, which is the `Options` struct field (`crud/options.go:40`) and not something a caller can write**, so a Spring Data reader following that line does not compile; `@Query` is the escape ladder's third rung; a `Sort` on the `Pageable` is a separate `crud.OrderBy` option. The subquery-per-predicate habit is H-SPECS-22 and is the one that ports *silently* wrong.
Guarantee 3 is the real one. `combine` (`spec.go:142-156`) is left-associative and `Or` widens the whole composition so far, so `Where(a).And(b).Or(c)` is `(a AND b) OR c`. A consumer who means `a AND (b OR c)` writes the chain left to right and gets a filter matching every row `c` matches — a silent widening, the failure mode this package's own tests are built around. The associativity is pinned at `specs_test.go:85-97`; the spelling for the other grouping is a nested `AnyOf` and it appears nowhere in this file's ancestors or in `docs/modules/en/specs.md`.
One smaller edge in the same hour: `CountBy` and `ExistsBy` take no options at all (`executor.go:73`, `:78`), where `FindAll` and `FindPage` do — so "count the filtered set, distinct" has to be rewritten as `users.Count(ctx, specs.As(sp), crud.Distinct())`, dropping out of the executor entirely. The fix is `opts ...crud.Option` on both, source-compatible with every existing call site.
**If not ready:** one section in `docs/modules/en/specs.md` — five bullets and the precedence sentence with its `AnyOf` spelling. It is the cheapest thing in this release and it is the difference between a good first day and a support thread.

### H-SPECS-17 — Exclude a value from a column that is sometimes null
**Who:** the engineer writing the intro's own filter — "active, in this tenant, **not archived**"
**Wants:** every row that is not archived, including the ones that were never archived at all.
**Story:** `Article.Status` is nullable and null means "never went through the workflow". They write `Article_.Status.Ne("archived")`, review it, ship it, and the feed silently loses every article that was never touched. Nothing errors. Three weeks later someone notices the counts do not add up.
**Must hold:**
1. Excluding a value still returns the rows that have no value — or the call site says it does not.
2. The same rule for a list: "not one of these ids" still returns the rows with no id set.
3. Negating a whole composition behaves the same way, or the difference is written where the negation is written.
4. Where the library means the other thing, the spelling that means what the consumer meant is reachable from the operator that does not.
**Today:** ❌ — 1, 2 and 3 are false, and nothing anywhere says so; 4 has no signpost.
**Evidence:** `Attr.Ne` (`metamodel.go:58`) routes to `crud.Ne`, which for a non-nil value builds `cmpNode{field, "<>", value}` (`crud/predicate.go:409-414`). SQL's `<>` against `NULL` is unknown, so the row is not returned. `Attr.NotIn` (`metamodel.go:68-70`) renders `NOT IN`, same rule. `Composite.Not` (`spec.go:131-140`) renders `NOT (…)` (`crud/predicate.go:337-347`), same rule again.
**The typed API makes this harder to notice than the string form, not easier.** `Attr[M, T].Ne` takes a non-nil `T`, and `ElemType` strips the pointer and the `Opt` wrapper before the type check (`crud/meta.go:521-530`, `metamodel.go:257`), so `T` is never a pointer and `crud.Ne`'s nil → `IS NOT NULL` branch is unreachable through the metamodel. A consumer who writes `crud.Ne("Status", nil)` by hand gets the null-aware node; a consumer who writes `Article_.Status.Ne(x)` cannot.
Nothing tests it and nothing could, on the current fixtures: the integration suite's `Attr.Ne` case is `User_.Active.Ne(true)` and its `Attr.NotIn` case is `User_.Name.NotIn("user 1")` (`test/integration/suite.go:504-505`), both over ten rows with no null in the column. `docs/modules/en/specs.md` says nothing about nulls; neither do the constructors' doc comments.
**If not ready:** the consumer writes `specs.AnyOf(Article_.Status.Ne("archived"), Article_.Status.IsNull())` and has to know to. Two fixes and both are cheap. The sentence: name the three-valued rule on `Ne`, `NotIn` and `Not`, in `crud` and in the module doc, with the `AnyOf` spelling beside it — this is the single most common wrong answer a SQL filter builder produces and it produces no error anywhere. The operator: `NeOrNull(v T)` and `NotInOrNull(vs ...T)` on `Attr`, three lines each, composing two existing nodes, no AST change. Whether the *default* should flip is a bigger question and this sweep does not propose it: `<>` is what SQL means and a library that quietly rewrote it would be lying in the other direction.

### H-SPECS-18 — Show an admin what they deleted, and let them put it back
**Who:** the engineer who declared `SoftDelete` because product asked for an undo
**Wants:** "list what this tenant deleted in the last 30 days", written with the same filters as every other list.
**Story:** They declare `SoftDelete("DeletedAt")`, wire `DeleteBy`, and ship. Product asks for a trash screen. They write `Article_.DeletedAt.NotNull()`, run it, and get an empty list. No error, no warning, and the rows are visibly in the table.
**Must hold:**
1. A filter can ask for the deleted rows.
2. Doing so does not silently return nothing.
3. If it is deliberately impossible, the API says so where the consumer looks for it.
**Today:** ❌ — 1 has no spelling; 2 fails; 3 has nothing.
**Evidence:** `resolveSoftDelete` folds `crud.IsNull(deletedAt)` into the repository's **permanent scope** (`crud/sqlrepo/blueprint.go:219-221`), and by [[D-004]] a specification can only AND onto it — `edge_test.go:257` is the test that proves a specification cannot escape a scope, and here that correctness is what closes the door. `Article_.DeletedAt.NotNull()` compiles, renders, and returns zero rows every time, because the statement carries `deleted_at IS NULL AND deleted_at IS NOT NULL`. There is no include-deleted option anywhere: `grep -rni 'includedeleted\|withdeleted\|withtrashed' --include='*.go' crud/` returns nothing.
This is structural rather than a missing operator, which is why it belongs in a happy-path sweep: H-SPECS-07 covers the write half (a `DeleteBy` becomes a stamp) and H-SPECS-10 covers naming the column without a literal, and the read half — the one a consumer reaches in week two — has no case anywhere and no answer in any doc.
**If not ready:** the presumed answer is a second `sqlrepo.Define` over the same table without `SoftDelete`, bound to the same source, with the trash filters written against that repository. It has a real property worth stating: the tombstone repository is a *different value*, so a handler cannot reach the deleted rows by accident, which an `IncludeDeleted()` option would not give. But it is a design instruction and it appears in no guide, no module doc and no use case. Write it down, or add the option and say what stops it from being passed by a wire client.

### H-SPECS-19 — Export the whole filtered set
**Who:** the engineer wiring the CSV export the intro names as one of the three call sites
**Wants:** every row the shared filter selects, streamed to a file.
**Story:** They reuse `publishedInTenant(t)`, call the find-all variant with no paging because they want all of it, and it works. Two years later the largest tenant has two million rows.
**Must hold:**
1. Asking for the whole filtered set is a call that says so.
2. Whatever the size, the failure is not the process falling over without warning — or the call site names the ceiling.
3. The paged escape does not carry a precondition the expression cannot state.
**Today:** 🟡 partial — 1 holds; 2 has nothing; 3 fails.
**Evidence:** `FindAll` passes the caller's options straight through (`executor.go:62-64`), and `GetAll` deliberately skips `MaxLimit` when no paging option is present (`crud/sqlrepo/repository.go:271-280`), with the comment saying why truncating would be worse: "the decorators that read a whole set in order to check it would check the first n and let the rest through". That is the right decision for the contract, and it means two million rows are materialised into one slice with nothing between the caller and the OOM.
The asymmetry is the finding. The write half has a guard rail against the accidental whole-table statement, imperfect as H-SPECS-07 shows; the read half has none. And the 90k-element `in` list of H-SPECS-09 gets a blocker row and a DX row while the 2M-row read is the one that arrives with no upstream service involved.
Guarantee 3: the recommended escape is `crud.After` cursor paging, and [[D-028]] refuses a cursor unless the sort ends in the primary key. A sort term taken off the metamodel (`metamodel.go:73-74`) cannot know that, so `crud.OrderBy(User_.CreatedAt.Desc())` plus `crud.After(c)` is a precondition the expression does not enforce and the module doc does not mention. The consumer who follows the advice hits a second refusal.
**If not ready:** `crud.Page`/`crud.Limit` in a loop, or `crud.After` with `User_.ID.Asc()` remembered as the last term. The cheap half is a sentence on `FindAll` saying it is unpaged by contract, naming the two paging options, and repeating [[D-028]]'s precondition in the module doc's paging paragraph. Whether the library should offer a streaming read at all is a `crud` question and out of this module's remit; what is in remit is that a consumer reaches the cliff through `FindAll` and there is no sign on it.

### H-SPECS-20 — The upstream list came back empty, on a read
**Who:** the same engineer as H-SPECS-09, on the verb they call a hundred times more often
**Wants:** "the orders for these customer ids" to answer honestly when the id list is empty.
**Story:** The upstream errors and returns an empty page rather than a failure. The filter is `In(ids...)` in one place and `NotIn(ids...)` in another. Both run. One page says "no results"; the other says "here is the whole table".
**Must hold:**
1. An `in` over an empty list matches nothing rather than failing to parse.
2. The caller can tell "nothing matched" from "the list I filtered on was empty".
3. The sibling `not in` over the same empty list does not invert to every row.
**Today:** 🟡 partial — 1 holds; 2 has nothing; 3 fails, and it is the read half of blocker 1.
**Evidence:** one node answers both, in opposite directions. `inNode.render` degrades an empty list to `1 = 0` for `in` and `1 = 1` for `not in` (`crud/predicate.go:201-209`). H-SPECS-02 guarantee 4 blesses the first arm as correct and it is.
**The rule is documented on exactly the two constructors a metamodel user never calls.** `crud.In` says "An empty list is always false" and `crud.NotIn` says "An empty list is always true" (`crud/predicate.go:444-450`). `Attr.In`/`Attr.NotIn` (`metamodel.go:64-70`) carry no doc comment at all, and the typed-slice constructors they route through say only "In for a typed slice" (`crud/predicate.go:452-459`). So the sentence exists, and it is on the string-addressed spelling this module was written to replace.
On a read, `User_.ID.NotIn(ids...)` over an empty `ids` is `WHERE 1 = 1` — the whole table, status 200, and a page that looks like data. On a write it is the blocker. Same node, same production input, and only the write half has anybody looking at it.
Guarantee 2 has nothing to branch on: `FindAll` returns `([]M{}, nil)` and a `Count` returns `(0, nil)`, identically to a filter that genuinely matched nothing.
**If not ready:** the consumer checks the length of their own slice before building the filter — the check they came here to stop writing, now needed on both verbs. The tautology answer proposed for blocker 1 does not reach this case: a read over `1 = 1` is a legitimate request. What does reach it is moving the sentence to where it is read — onto `InAny`/`NotInAny`, onto `Attr.In`/`Attr.NotIn`, and into the module doc — and, if the owner wants more, `NotInAny` refusing an empty slice outright rather than inverting. That last one is a `crud` behaviour change with a round-trip consequence (`crud/query/roundtrip_test.go:71` pins `"nin of nothing"`), so price it as one.

### H-SPECS-21 — Narrow the children a preload loads, per request
**Who:** the engineer on the article feed, one screen past H-SPECS-15
**Wants:** "on this screen, load only the approved comments" — a request-time narrowing, not a declaration.
**Story:** They find `crud.PreloadWhere`, and now they have two metamodels in scope: the article's and the comment's. They pick one. It compiles.
**Must hold:**
1. The inner filter is written against the child model, and handing it the parent's filter does not compile.
2. Where parent and child share a column name, the mistake is still caught.
3. The relation path itself is an identifier rather than a literal.
**Today:** ❌ — 1 and 2 fail; 3 holds.
**Evidence:** `crud.PreloadWhere(path string, opts ...Option)` (`crud/preload.go:43`) takes plain options, and `specs.As[M]` erases `M` into a bare `crud.Option` (`spec.go:204`). So `crud.PreloadWhere(Article_.Comments.Path(), specs.As(Article_.TenantID.Eq(t)))` compiles with no type check of any kind. The option is applied to the child's own statement, so where the two models share a column name — `TenantID`, `Active`, `CreatedAt`, which are exactly the columns people scope on — it narrows the child on the child's column and answers 200 with the wrong children. Where they do not share a name, it is a per-request `*crud.UnknownFieldError` → 400 (`crud/relation.go:378`, `port/kind.go:123`): a fourth [[D-021]] proviso miss, reached through the typed API.
H-SPECS-15's guarantee 1 covers the other direction — a consumer picking the relation filter when they meant the child narrowing. This is the direction with no error and no warning.
Guarantee 3 holds through `Rel.Path()`/`RelPath()` (`metamodel.go:163-167`), proven at `edge_test.go:322`.
**If not ready:** the consumer remembers, every time, on a call where the compiler would happily catch it. Closing 1 and 2 is three lines on `Rel`, and no new AST node: `Rel[M, T]` already carries the target model as its second type parameter and is checked against the relation at bind time (`metamodel.go:281-291`), so `func (r Rel[M, T]) PreloadWhere(s Specification[T]) crud.Option` can build `crud.PreloadWhere(r.path, crud.Where(Predicate(s)))` and will not accept a `Specification[M]`. The call site becomes `Article_.Comments.PreloadWhere(Comment_.Approved.Eq(true))` — shorter than what it replaces and wrong-model-proof. It inherits the shadowing hazard `Path`/`RelPath` already pay for, so emit `RelPreloadWhere` beside it the way the module already does.

### H-SPECS-22 — Articles with a comment that is both approved and not spam
**Who:** the engineer writing the moderation view, and every Spring Data reader on their first relation filter
**Wants:** articles that have *one* comment satisfying both conditions.
**Story:** They write `specs.AllOf(Article_.Comments.Approved.Eq(true), Article_.Comments.Spam.Eq(false))`. It compiles, it reads exactly like the sentence they were given, and it passes review. It answers a different question.
**Must hold:**
1. Two conditions over the same to-many relation ask about one child — or the difference is written down where the filter is written.
2. If the library means the other thing, there is a spelling for the one the consumer meant.
**Today:** ❌ — 1 and 2 have nothing.
**Evidence:** `writer.leaf` opens a fresh `EXISTS (SELECT 1 FROM …` for every leaf that has hops (`crud/predicate.go:86-128`), so two leaves on one path never share a subquery. The filter above is "has an approved comment AND has a non-spam comment": an article with one approved spam comment and one unapproved clean comment matches, and the moderation view shows it. Status 200, no error, and the answer is *wider* than the sentence — this file's recurring failure mode.
It is precisely JPA's subquery-per-predicate behaviour, so a Spring Data reader arrives with the wrong expectation already formed and is not corrected. [[D-005]] records the one-EXISTS-per-leaf shape and does not say what two of them mean together; `docs/modules/en/specs.md` does not mention it at all; nothing in the tree tests it.
Guarantee 2's nearest spelling today is the third rung — `specs.Lift[Article](crud.Raw(...))` with the correlated subquery written by hand, giving up the rename safety and, by [[D-054]], the wire.
**If not ready:** the consumer ships a filter that is wider than it reads, and the widening grows with the number of conditions. The cheap half is a paragraph beside the relation examples, naming the failure with the two-comment case — it is a better warning than the one already there, because it has no visible tell at the call site. The real fix is a spelling that groups leaves onto one path, `Article_.Comments.Any(Comment_.Approved.Eq(true), Comment_.Spam.Eq(false))`, and it is a **`crud` writer change and not a `specs` one**: the grouping has to reach `writer.leaf` so the two conditions land inside one `EXISTS`. Price it there, and note that it also gives `Rel.Exists()` its natural home — `Any()` with no arguments.

## The DX this should have

### The call site

```go
users := specs.Executor(Users.Bind(db))

active, err := users.FindAll(ctx, User_.Active.Eq(true))
```

Two lines, no explicit type parameters, no builder to construct, no session to
open. `User_` is generated, so the first line of the file is already useful.
**This is what exists today** — the short path is not the gap.

### Turning one knob

```go
// the same list, from an admin form where four of the five filters are blank
sp := specs.AllOf(
    User_.TenantID.Eq(tenant),                          // mandatory, and load-bearing: see below
    User_.Name.ContainsIgnoreCase(f.Q),                 // no-op on "" — neither operator exists today
    User_.OwnerID.EqPtr(f.OwnerID),                     // *int64
    User_.ManagerID.EqOpt(f.ManagerID),                 // crud.Opt[int64]
    specs.If(f.IncludeArchived, User_.Status.Eq("archived")),
)

page, err := users.FindPage(ctx, sp,
    crud.Page(f.Page), crud.Limit(25), crud.OrderBy(User_.CreatedAt.Desc()))
```

Every line applies left to right, which is the point. Round 1's sample put
`specs.IfSet(f.OwnerID, User_.OwnerID.Eq)` — an unapplied method value — directly
below a line that applies one, and the dx reviewer is right that two adjacent
lines reading in opposite directions is worse DX than the problem being solved.
Putting the optionality on the *attribute* is roughly six lines in
`metamodel.go`, needs no generator change and no AST node, and gives
`crud.Opt`'s third state somewhere to go: undefined contributes nothing, null
contributes `crud.IsNull`, set contributes `crud.Eq`. Flattening null into
"contributes nothing" would make "clear the owner" and "leave the owner alone"
the same filter, which is the distinction [[D-002]] exists to keep — and it is
not hypothetical, because **all six** fields of this repository's own generated
update DTO are `*T` or `crud.Opt[T]` (`_examples/example/blog/vv_gen.go:20-25`;
round 1 said four of six).

`If` stays for the genuine condition, and it carries two warnings that have to
ship with it:

- **Inside `AnyOf`, all-absent means every row.** `fold` returns nil when no
  member yields a predicate (`spec.go:174-190`) and `AnyOf`'s doc comment says an
  empty list is no restriction. So `specs.AnyOf(If(a, x), If(b, y))` with both
  conditions false widens to everything — which is exactly what H-SPECS-02
  guarantee 3 promises never happens, satisfied today only because an
  all-absent OR is hard to reach by hand. Either `If`'s doc comment says this, or
  `AnyOf` gets an OR-safe sibling that folds to `crud.False()` instead.
- **With every member absent, the whole filter is nil.** `FindAll` then renders
  no `WHERE` and no `LIMIT` (H-SPECS-19), so the admin form where four of five
  fields are blank becomes the form where five of five are blank the first time
  somebody bookmarks the bare URL. That is why `User_.TenantID.Eq(tenant)` leads
  the sample: a mandatory member ahead of the optional ones is the guard, and it
  is worth saying rather than leaving as decoration.

The property all of this rests on — that the fold drops nil members — **is stated
nowhere today**. `AllOf`'s doc comment (`spec.go:168`) says only "an empty list
means no restriction". Saying it on `AllOf` and in the module doc is worth doing
whether or not any of these constructors land, and it is what lets a consumer
write the four-line `If` in their own package in the meantime.

And the rungs below, for the operators the metamodel has no name for, without
leaving the expression:

```go
sp = sp.And(specs.Lift[User](crud.Gt(User_.Age.Name(), 18)))       // rung 2, today
sp = sp.AndPredicate(crud.Raw(`"payload" @> ?`, needle))           // rung 3, proposed
```

Two things there. First, `sp.And(...)` and not `specs.Where(sp).And(...)`:
`AllOf`, `AnyOf` and `Where().And()` all return `Composite[M]`
(`spec.go:107-130`), so the composition never has to be restarted, and round 1's
redundant `Where` made reaching the next rung look like re-entering the
composition — the exact impression this section exists to dispel.

Second, the ladder has an uncounted cliff and it should be closed rather than
excused. `specs.Lift[M any](p crud.Predicate)` (`spec.go:47`) has no argument to
infer `M` from, so **every** rung-2 and rung-3 call site spells the model type
again, in a section whose opening line is "no explicit type parameters".
`func (c Composite[M]) AndPredicate(p crud.Predicate) Composite[M]` and an `Or`
twin are three lines each in `spec.go`, add no node, and infer `M` from the
receiver. That is the difference between an escape ladder and a step down.

And the shape H-SPECS-04 and H-SPECS-22 want:

```go
sp = sp.And(Order_.Lines.RelExists())                  // "has at least one line"
sp = sp.And(specs.Not(Order_.Lines.RelExists()))       // "has none"
```

`Rel[M, T]` already knows its target model and is checked against it at bind time
(`metamodel.go:281-291`), and `crud.Schema` exposes `PK` (`crud/meta.go:94`), so
this can emit `IsNotNull(path + "." + targetPK)` — the predicate that already
renders the right correlated `EXISTS`, with the reading fixed. Three points on
the shape:

- **Spell it `RelExists()`, or emit `Exists()`/`RelExists()` as a pair.** The
  handle is embedded and every target column is a field one level nearer, which
  is why `RelPath` exists at all (`metamodel.go:154-160`). A target model with a
  column called `Exists` shadows the promoted method and breaks that one
  relation. A new promoted method that ignores this re-opens a case the module
  has already paid for once.
- **Ship the negative in the same doc comment.** "Has no lines" is
  `specs.Not(Order_.Lines.RelExists())`; a consumer who instead writes
  `Order_.Lines.ID.IsNull()` gets `EXISTS (… AND id IS NULL)`, which is a
  different question that matches nothing. Name it as the trap, the way
  `docs/modules/en/specs.md:148` already names the `Article_.Comments.Approved`
  one.
- A new EXISTS-shaped AST node would be the expensive alternative: it owes a
  `document` method by [[D-054]], and then either a wire-DSL spelling or a
  by-name refusal.

### Why this shape

The ceremony budget is spent before the consumer arrives: the metamodel is
generated, the type parameters are inferred from the argument, and the executor
is a decorator over the repository they already built. So the first useful line
is the first line, and nothing has to be constructed, configured or closed.

What earns the design is the ladder, not the top rung. Three rungs — typed
attribute, `Lift` over a bound name, a raw fragment — and every one of them
returns the same `Specification[M]`. Reaching further never means restarting the
expression, which is the property most query builders lose exactly when you need
it: the moment you want one thing the fluent API cannot say, you are back to
string concatenation for the whole clause. Today that claim costs an explicit
type parameter at every rung-2 call site, which is why `AndPredicate` is proposed
above rather than described as a nicety.

Rung 3 is a one-way door and should be scored as one rather than as a
convenience. `crud.Raw` does not resolve or quote identifiers and [[D-003]]
forbids making it — "half-resolution is worse than none" — so there is no way to
interpolate a resolved column into a raw fragment. A rename will not catch it,
and [[D-054]] refuses to marshal it, so the filter also stops crossing a process
boundary. That is a fine trade; it is not a small one.

The alternative shapes cost more than they look. A `map[string]any` filter is
shorter to write and gives back nothing: no rename safety, no composition, no
type check. A full expression-tree builder in the Java style is longer at every
call site and buys nothing this does not already have. A per-model generated
fluent builder (`Users.Query().Active(true).AgeGte(18)`) reads better for one
model and cannot express "or" without growing a second grammar.

And one shape that already exists and should be named rather than competed with:
for a filter that *arrives from a request*, `crud/query` compiles one JSON
document into `crud.Options` with no per-resource code at all. This module is for
filters the codebase names and reuses. Neither module's docs draw that line, and
without it the twentieth resource hand-writes an `AllOf` block it did not need.

### What it must not break

- [[D-054]] — **challenged twice, and both challenges are load-bearing rather
  than incidental.**
  **(a) The tautology guard H-SPECS-07 wants cannot be built without it.** The
  guard has to fire in two packages outside `crud` — `specs.DeleteBy`/`UpdateBy`
  (`executor.go:84`, `:94`) and `security.gate.DeleteAll`
  (`crud/decorators/security/security.go:724`) — so an unexported method on
  `Predicate` is not enough on its own; something exported has to answer. D-054's
  "What it forbids" opens with "Do not add an exported way to inspect a
  `Predicate` from outside `crud`. This is the one, it is one-directional, and
  [[D-004]] rests on the AST staying closed." The narrowest shape that works is
  `func (o Options) Unbounded() bool` on the already-exported `crud.Options`,
  answering one bit and exposing no node: `security.go:724` is one edit from it
  (it already calls `crud.Build(opts...).Predicate()`) and `specs` reaches it
  through `crud.Where(p)`. Internally it is D-054's own shape — a second
  unexported method on the `Predicate` interface, so a new node cannot forget to
  answer (`crud/document.go:8-11` states that rationale). **This is a challenge
  to the letter of D-054 and not to its reason**: one bit cannot take a predicate
  apart or rebuild it, which is what D-004 rests on. It still needs an exception
  recorded in D-054, in the same change, or it must not ship. And say plainly in
  the same change that `crud.Raw("1=1")` is outside any such guard by
  construction, and that `DeleteAll`/`UpdateAll` remain the deliberate way to
  mean every row — a partial guard sold as a total one is the same shape as the
  bug.
  **(b) The `ESCAPE` fix has to be unconditional.** Round 1 proposed emitting the
  clause "whenever the pattern came from an escaping constructor", which needs a
  provenance field on `likeNode` — and that field does not survive the wire.
  D-054 records the round trip explicitly: "`crud.Contains(f, s)` is already
  `likeNode` with an escaped pattern by the time the marshaller sees it, so it
  goes out as `like` with that pattern and compiles back to the identical node."
  `likeNode.document` emits `like` with the pattern alone (`crud/document.go:199-217`)
  and `buildText` compiles it back through `crud.Like`
  (`crud/query/filter.go:275-290`), which would set no flag. So a filter that
  escaped correctly in-process would stop escaping the moment it crossed
  `remote`, and `crud/query/roundtrip_test.go:67` — the case `"contains escapes"`,
  which asserts rendered SQL equality — fails on the first run. Emitting the
  clause for every `likeNode` keeps the round trip byte-identical, and it is also
  what makes the second fix work at all: under a provenance rule, a pattern run
  through an exported `EscapeLike` and handed to `crud.Like` would still get no
  clause and still be wrong on SQLite.
  The third inheritance of D-054 is unchanged and unchallenged: `MarshalPredicate`
  refuses `Raw`, `EqField` and `False` by name, which costs the unit-test route
  (H-SPECS-11) and the transport (H-SPECS-12) their third rung. Both docs have to
  say so.
- [[D-015]] — **this shape has to fix a live breach, not merely avoid one.** The
  invariant is that every failure a caller branches on is reachable with
  `errors.Is` against an exported sentinel in package `crud`. `ErrUnboundedDelete`
  and `ErrUnboundedUpdate` wrap nothing, and D-015's own "Where it lives" lists
  them at `crud/decorators/specs/errors.go` without recording the exception, so
  the decision and the tree disagree today. There is no `crud.ErrBadRequest` to
  wrap: `crud/errors.go` declares `ErrNotFound`, `ErrNoTxSupport`, `ErrMissingID`,
  `ErrReadOnly`, `ErrForbidden`, `ErrConflict` and `ErrStaleVersion`.
  **The cheapest correct shape is one the tree already uses**: declare
  `ErrBadRequest` in `crud` and re-point `port.ErrBadRequest` at it —
  `var ErrBadRequest = crud.ErrBadRequest`, `port` already importing `crud` —
  which is exactly the move `port/porthttp/errors.go:10-14` makes and explains,
  "the same variable … and not a copy of it: two sentinels would each be
  invisible to the other's mapping". Done that way it costs **no new arm** in
  `sentinelKind`: `port/kind.go:123` already ORs `errors.Is(err, ErrBadRequest)`
  with three `errors.As` matchers in one case, so the existing arm matches
  unchanged. Round 1 priced this as a new arm and made the cheaper option look
  dearer while the two were being weighed. It still costs a row in D-015's table
  and an edit to its "Where it lives" entry, and it changes the message text
  behind every binding-produced bad request, which is worth checking against the
  renderer's tests.
  Two alternatives, both worse and both worth rejecting on the record: having
  `specs` import `port` inverts the direction every other decorator uses and puts
  a transport-shaped package under a repository decorator (and package `crud`
  may not import `port` at all — `Makefile:TIER0_STDLIB`); and raising an `errs`
  fault carrying `KindBadRequest`, which `KindOfWith` reads before any sentinel
  (`port/kind.go:36-45`), buys the status code and loses the `errors.Is` that
  D-015 is about.
- [[D-003]] — the predicate AST is closed. `EqPtr`, `EqOpt`, `If`,
  `AndPredicate`, `RelExists`, `PreloadWhere` on `Rel`, `NeOrNull` and the
  case-insensitive constructors all compose existing nodes; none introduces one.
  Adding an unexported method to `Predicate` is not what D-003 forbids; exporting
  `render` or adding `SQL() string` is, and none of this does either.
- [[D-004]] — `Where` ANDs and never replaces. Nothing proposed here offers a
  "replace the repository scope" spelling, and `edge_test.go:257` is the test
  that would catch one. H-SPECS-18 is that rule working rather than bending: the
  soft-delete read is closed *because* a specification cannot widen a scope, and
  the answer is a second repository, not a weaker `Where`.
- [[D-005]] — a relation filter is a correlated `EXISTS`. `RelExists()` renders
  that shape by construction. The `Any(...)` grouping H-SPECS-22 asks for does
  not challenge it either — it is still one `EXISTS`, with more inside it — but
  it does change what `writer.leaf` emits, so it belongs in `crud` with D-005
  updated to say what two conditions on one path mean.
- [[D-013]] — an unknown field is a rejection, and the string-addressed form
  stays. That is why H-SPECS-03's guarantee 1 cannot be made total, and it should
  not be.
- [[D-018]] — metamodels are generated. Nothing in the DX section above needs a
  generator change. A metamodel coverage assertion, the domain-type attribute
  choice (H-SPECS-10) and withholding `Desc()` inside a to-many group
  (H-SPECS-04) all do, and all three are inside this decision's remit rather than
  against it — the third of them is *only* implementable there.
- [[D-019]] — a dialect difference must not be observable except where this file
  names it. There are eleven named. **The missing `ESCAPE` clause is a twelfth
  and is not named**, so H-SPECS-08's blocker is a breach here as well as a
  breach of [[UC-007]] guarantee 9. Emitting the clause closes it; documenting it
  as a twelfth difference does not, because the guarantee it falsifies is a
  use-case guarantee and not a convenience. Difference 1 (nulls ordering) and
  difference 4 (`LIKE` collation) are both filed, both correct, and both already
  named at the call site or in D-019's own cost column.
- [[D-021]] — magic is preferred to orthodoxy *provided it fails at build or
  start-up*. **Four places in this module do not meet the proviso**, and round 1
  counted two: the missing coverage check (H-SPECS-03 guarantee 5); the to-many
  `Desc()` that compiles and fails per request (H-SPECS-04); an `attr:"-"`
  attribute that keeps every method and renders `crud.Eq("", v)` (H-SPECS-03);
  and a specification for the wrong model handed to `crud.PreloadWhere`
  (H-SPECS-21), which fails per request where the names differ and does not fail
  at all where they match.
- [[D-028]] — a cursor is the sort tuple and is refused unless the sort ends in
  the primary key. Sort terms here come off the metamodel (`metamodel.go:73-74`),
  which cannot know that, so a consumer paging a large export with `crud.After`
  has a precondition the expression does not enforce (H-SPECS-19). Nothing
  proposed changes it; it belongs in the module doc.
- [[D-050]] — the generator reads source text, not types. That is why the
  domain-type fix in H-SPECS-10 reaches same-package declarations only, and why
  an imported `decimal.Decimal` needs an explicit escape rather than inference.

**Not everything above is a new finding, and an owner's time at a release goes to
the new ones.** Already filed and re-raised for visibility: the metamodel
coverage gap ([[UC-007]] caveat 2, assigned to [[UC-014]]); the cross-package
relation ([[UC-007]] and [[UC-014]] both list it out of scope); the sentinels
wrapping nothing ([[UC-007]] Status, though **not** as the D-015 exception it is);
the update-by test thinness ([[UC-007]] Status); nulls ordering ([[D-019]]
difference 1); `Contains`' collation-dependent case behaviour ([[D-019]]
difference 4); and — corrected from round 1, which called it new — the
domain-type attribute fallback, which [[UC-014]] records at lines 124-127 as
"one smaller thing, still open" though it is absent from the Index's open list.
New here: the tautology walking both guards; the missing `ESCAPE` clause and the
`Builder`'s complete lack of an escaped operator; negation's treatment of NULL;
the soft-delete read half; two conditions on one to-many relation; the wrong
model handed to `PreloadWhere`; the unbounded export; the uncapped `in` list; the
`not in` of nothing on a read; the to-many `Desc()`; the `attr:"-"` attribute;
the untested gate/executor pair; the `FindPage` total whose test cannot fail; and
the `DISTINCT`-plus-relation-sort collision a caller cannot avoid.

## DX verdict

"Code" and "Docs" are separated because they have different owners
(`crud`/`cmd/vv` versus `docs/`) and very different risk, and a release plan
wants to sort by one of them.

| What the ideal asks for | Today | Code | Docs |
|---|---|---|---|
| Name a filter, compose it, pass it around | exactly this — `specs.Of`, `sp.And()`, `AllOf`, and `And` returns something combinable again | none | none |
| Typed field references, generated | one line per model in `vv_gen.go`; `User_.Age.Gte(18)` | none | none |
| Optional filters from a form | no `EqPtr`, no `EqOpt`, no `If`; a `var parts []specs.Specification[User]` accumulator — **17 lines for five filters against 8**, and the assembly stops being an expression | small | small |
| A filter that arrives from a request | `crud/query` already does it with no per-resource code; nothing says so | none | small |
| Escaped `LIKE`, typed form | `Contains` escapes the argument and no `ESCAPE` clause is emitted; correct on PostgreSQL and MySQL by their defaults, silently wrong on SQLite | large | small |
| Escaped `LIKE`, criteria form | `Builder` has `Like`/`NotLike`/`LikeIgnoreCase` and nothing else, so the form the module doc teaches first cannot escape at all, on any dialect | small | small |
| Case-insensitive contains | `LikeIgnoreCase` with a pattern you build and escape yourself, except `escapeLike` is unexported | small | small |
| "Not archived" over a nullable column | `Ne` excludes the nulls; the working spelling is a two-member `AnyOf` and nothing anywhere says so | small | large |
| A range filter on `type Cents int64` | `specs.Lift[Order](crud.Gte(Order_.Total.Name(), low))` — keeps rename safety, loses the value-type check; hand-editing `vv_gen.go` is lost at the next generate | small | small |
| Filter across a relation | `Article_.Comments.Approved.Eq(true)`, one expression — same-package targets, within `-depth`; past either, the literal form still works | none | small |
| Two conditions on one to-many | two independent `EXISTS`, so it means something wider than it reads, with no spelling for the narrow question | large | small |
| Sort across a relation | to-one: one expression. to-many: **compiles, fails per request**. With `crud.Distinct()`: a 400 the caller cannot avoid | large | none |
| "Has at least one child" | `Order_.Lines.ID.NotNull()` — right SQL, wrong reading; the negative reads worse | small | small |
| Narrow the preloaded children | `crud.PreloadWhere` takes plain options, so the parent's filter compiles into the child's statement | small | small |
| Find exactly one, safely | `FindOne`, and caller paging cannot disarm it | none | none |
| Bulk write behind a guard rail | `UpdateBy`/`DeleteBy` — the nil guard is exhaustive, the tautology guard does not exist in either layer, and the sentinels wrap nothing | large | small |
| Count or exist with an option | `CountBy`/`ExistsBy` take none, so "count the filtered set, distinct" drops out of the executor entirely | small | none |
| Bulk write inside a transaction | `crud.InTx` or `users.Tx`, no rewiring — and no doc, no test | none | large |
| Read the rows you soft-deleted | no spelling; the tombstone test is in the permanent scope and the answer is a second `Define` nobody has written down | none | large |
| Export the whole filtered set | `FindAll` is unpaged by contract with no ceiling and no sign; the cursor escape has [[D-028]]'s precondition | none | large |
| An `in` over 90k ids | built and sent; the driver answers, as a 500 | small | small |
| `not in` over an empty list, on a read | `WHERE 1 = 1` — the whole table, 200 | none | small |
| Unit-test the filter | `crudtest.Postgres()` + `rec.Last().SQL`, six lines — no extra module, use-cased, and not linked from this module's docs | none | small |
| See what it compiled to in production | no logging seam anywhere in `sqlrepo`; rebuild it in a test, or marshal it and lose the `Raw` rung | none | small |
| Reach past the metamodel | `Lift` over `attr.Name()` in one line, but `M` is spelled at every call site; `Raw` gives up rename safety and the wire, permanently and by decision | small | small |
| Use the filter outside the executor | `specs.As` / `specs.Predicate`, one call each — with the handler scope reaching reads only | none | small |
| A stale metamodel is noticed | a renamed column: at build. A *new* column: only by the drift test, which [[UC-014]] guarantee 13 says is a start-up refusal | small | small |

**Overall:** the algebra and the executor read like the ideal — the shortest
useful call is two lines, the composition rules are the ones a consumer would
guess, `And` gives back something combinable, and the escape ladder means
customising extends the short path instead of abandoning it, at the cost of one
explicit type parameter that three lines would remove. Where it gets wordy is the
boring middle: an optional-filter form needs an accumulator that also costs the
consumer the ability to return the assembly from a function. Where customising
means abandoning the short path is rung 3, and that is a deliberate one-way door
rather than a gap; `CountBy` with an option is the one place it is neither. What
should hold up the tag is none of that. It is that the module's two safety
features — the empty-filter guard and the escaping `LIKE` — each have a hole
shaped exactly like the input that reaches them in production, an empty upstream
list and a `%` in a search box, and that the two filters a consumer writes on
their second day, "not archived" over a nullable column and "a comment that is
both approved and clean", both answer a different question than they read.

## Release blockers found here

"First raised" is here because the verdict line has to be honest about how much
of this is new work: an owner triaging at a tag reads the new rows first.

| # | What | Severity | First raised | Owner | Why it blocks |
|---|---|---|---|---|---|
| 1 | `DeleteBy`/`UpdateBy` refuse only a *nil* predicate, and `security.Gate.DeleteAll` tests the same thing. `User_.ID.NotIn(ids...)` over an empty `ids` renders `1 = 1` and walks through both | blocker | new | crud (+ specs, security) | Two independent guards exist to stop an accidental truncate, and the commonest way a filter empties out in production — a list that came back empty — defeats both |
| 2 | `Contains`/`StartsWith`/`EndsWith` escape with backslash and emit no `ESCAPE` clause (wrong on SQLite), and `specs.Builder` has no escaped operator at all, so the literal form the module doc teaches first is wrong on every dialect | blocker | new | crud (+ specs) | Falsifies [[UC-007]] guarantee 9 and is an unnamed twelfth [[D-019]] difference; [[UC-007]]'s Status and its Index row still read "covered" |
| 3 | `ErrUnboundedDelete` and `ErrUnboundedUpdate` wrap no `crud` sentinel, so `port.KindOf` falls through to internal | blocker | [[UC-007]] Status | crud + specs | A live breach of [[D-015]]'s invariant, listed in its own "Where it lives" with no exception recorded — a refused bulk write reaches the client as 500 |
| 4 | `Ne`, `NotIn` and `Not` exclude rows where the column is NULL, and nothing in the module, the docs or the constructors says so | serious | new | crud + docs | The intro's own filter — "not archived" over a nullable column — silently drops every row that was never archived, with no error and no tell at the call site |
| 5 | Two conditions on one to-many relation compile to two independent `EXISTS`, so `AllOf(Comments.Approved, Comments.NotSpam)` means "has one of each" | serious | new | crud + docs | A silent widening with a 200 on it, and the exact habit a Spring Data reader ports in on day one; no spelling exists for the narrow question |
| 6 | A soft-deleting repository has no way to read its own tombstones: the test is in the permanent scope and [[D-004]] means a filter can only AND | serious | new | docs (+ sqlrepo, if an option is wanted) | The trash screen and the restore button are week-two work, and the filter that asks for them returns an empty list with no error |
| 7 | `Article_.Comments.Body.Desc()` compiles and fails per request with a `*crud.SchemaError` | serious | new | cmd/vv | The module's headline promise inverted, and [[D-021]]'s proviso unmet: the magic is there and the eager failure is not. Only the generator can fix it |
| 8 | `crud.PreloadWhere` takes plain options, so a specification for the *parent* model compiles into a child narrowing — silently wrong where the two share a column name | serious | new | specs (+ crud) | The columns people scope on are exactly the shared ones; a fourth [[D-021]] proviso miss, and three lines on `Rel` close it |
| 9 | `User_.ID.In(ids...)` has no cap, where `crud/query` caps at 1024 and `crud/preload` chunks at 900 | serious | new | crud + specs | The one `in` entry point with no guard is the one a consumer reaches for first, and it fails as a 500 after the statement was built |
| 10 | A domain type — `type Cents int64`, `type Status string` — is emitted as `specs.Attr`, so no range and no pattern operators | serious | [[UC-014]] :124-127 | cmd/vv | Any application with a domain type on a filterable column hits it in week one; `specs.Ord[Order, Cents]` already compiles, so it is a generator choice |
| 11 | No start-up check that the metamodel covers every column; only the regenerate-and-diff test notices | serious | [[UC-007]] caveat 2 | cmd/vv ([[UC-014]]) | [[UC-014]] guarantee 13 claims a refusal that reaches the update DTO and not the typed query API, so a doc says this is closed and it is not |
| 12 | Nothing tests a specification bulk write under the security gate | serious | new | test | "The offboarding job deleted another tenant's rows" is the worst outcome here; the behaviour is right by construction and pinned by nothing |
| 13 | `FindPage`'s total-counts-the-filtered-set assertion uses a filter matching all ten seeded rows | serious | new | test | The assertion passes unchanged with the specification dropped from the `COUNT`, so a verdict rests on nothing |
| 14 | `crud.Distinct()` plus a relation sort is refused before the `asked` check, so a repository `DefaultSort` off the metamodel makes `?distinct=1` a 400 the client cannot avoid | serious | new | sqlrepo | This module supplies both halves of the collision, and `distinctSort`'s own comment says a default sort should be dropped rather than turned into an unavoidable error |
| 15 | `FindAll` is unbounded by contract with no ceiling named at the call site, and the cursor escape carries [[D-028]]'s unstated precondition | sharp edge | new | docs (+ crud) | The write path has a guard rail against the accidental whole-table statement and the read path has none; the CSV export is one of the three call sites the module exists for |
| 16 | `not in` over an empty list renders `1 = 1` on a read too — the whole table, status 200. The rule is documented on `crud.NotIn` and on none of the typed spellings that route to it | sharp edge | new | docs (+ crud) | Blocker 1's read half, on the verb called a hundred times more often, with nothing for the caller to branch on |
| 17 | `UpdateBy` with a DTO that defines nothing returns `(0, nil)`; nothing says a bulk update bumps the version column and stamps no updated-at | sharp edge | new | docs (+ sqlrepo) | The regressed DTO builder and the filter that matched nothing log the same line, and a consumer with optimistic locking needs the version fact before the run |
| 18 | `UpdateBy`'s empty-filter refusal is tested for one spelling where `DeleteBy`'s is tested for thirteen | sharp edge | [[UC-007]] Status | specs | The guard is the same line, so the asymmetry is in the proof rather than the code |
| 19 | No conditional or optional-aware constructor, and nothing states that the fold drops nil members | sharp edge | new | specs | The commonest assembly job in an admin API is an accumulator written once per resource, and the property that would remove it is undocumented |
| 20 | A relation handle has no "exists at all" spelling, positive or negative | sharp edge | new | specs | "Has at least one child" is a not-null test on the child's key, which reads as something else in review; "has none" reads worse |
| 21 | `Where(a).And(b).Or(c)` is `(a AND b) OR c`, and the `AnyOf` spelling for the other grouping is written down nowhere | sharp edge | new | docs | A consumer who means `a AND (b OR c)` gets a silent widening, which is the failure mode this package's tests are built around |
| 22 | `specs.Lift[M]` cannot infer `M`, so every escape-ladder call site spells the model type again | sharp edge | new | specs | The ladder is the design's best property and it is the one place the module asks for an explicit type parameter; `AndPredicate` is three lines |
| 23 | `CountBy` and `ExistsBy` take no options | sharp edge | new | specs | "Count the filtered set, distinct" has to leave the executor entirely; `opts ...crud.Option` is source-compatible with every call site |
| 24 | Relation expansion stops silently at `-depth` (default 2) and at a package boundary — two `continue`s three lines apart, neither with a note in the output | sharp edge | [[UC-007]] out of scope | cmd/vv | Expressible after all, through the literal form or `Lift`, at the cost of the rename safety — so the fix is a note in the output rather than a feature |
| 25 | `docs/modules/en/specs.md` links `crudtest` nowhere, never mentions transactions, and lists no "what does not port from Spring Data" | sharp edge | new | docs | Three cheap paragraphs stand between a good first day and a support thread; the machinery they describe already works |
| 26 | `crud.SourceOf` cannot be called on a `specs.Repo` at all; the working route is `.Unwrap()` and nothing says so | sharp edge | new | crud + docs | [[D-061]] is the house rule for optional interfaces and the typed facade is the one place the rule's own helper does not apply directly |

## Contested

- **The verdict keeps three blockers, and the consumer reviewer's correction is
  taken in the "First raised" column rather than in the count.** Blocker 3 is
  already filed in [[UC-007]]'s Status — but not as the [[D-015]] exception it
  is, and the decision doc and the tree disagree on the record. A row that is
  filed in one place and contradicted in another is still a blocker at a tag.
  The paragraph that used to explain this is gone; the column does the work.
- **H-SPECS-01 keeps guarantee 1, which a reviewer called a tautology in an
  earlier round.** "Combining two filters yields a filter" is not a restatement
  of `And(Specification[M]) Composite[M]` — it is the claim that the *returned*
  type also satisfies `Specification[M]` (`spec.go:107`, `:113`), which is the
  closure property fluent builders routinely lose and which a consumer can check
  in one line: `var _ specs.Specification[User] = specs.Where(a).And(b)`.
  Guarantee 3 (the package boundary) is cut this round as asked.
- **Blocker 24 stays a row of its own rather than being folded into 11.** Two
  reviewers asked for it to be merged into the codegen item. It is a different
  consequence with a different fix — the depth stop hits every consumer and now
  has a known workaround, where the package boundary is a declared out-of-scope —
  and a specs consumer meets both inside specs, so folding it would hide the one
  that hits everybody behind the one that is already decided.
- **H-SPECS-15 keeps its case rather than shrinking to a cross-reference.** Two
  reviewers noted that `crud.PreloadWhere` and `sqlrepo.RelationScope` belong to
  the preload sweep, and the mechanism half is now explicitly handed off. What is
  kept is the consumer-facing half: which of two correct mechanisms a reader
  picks, and why the generated attribute is where they need the warning. The
  reviewers' sharper finding — the wrong model handed to `PreloadWhere` — became
  H-SPECS-21 rather than a bullet here, because its failure mode is silent where
  H-SPECS-15's is merely confusing.
- **`Contains`' case behaviour is kept as a filed difference rather than reported
  as a new defect.** A reviewer called it "a sharper defect than the one
  reported". It is sharper, and it is [[D-019]] difference 4, measured and
  written down with `LikeIgnoreCase` named as the portable answer. What is new is
  that the portable answer escapes nothing, and that is where blocker 2 sits.
- **Round 1's blocker 8 was withdrawn and its residue survives as row 26.** Both
  reviewers showed the stated consequence was impossible: `crud.SourceOf` takes a
  `Core[M, ID]` (`crud/executor.go:195`), and `crud.Repo`'s DTO-typed
  `Update`/`UpdateAll` (`crud/repo.go:80`, `:96`) shadow `Core`'s, so passing a
  typed repo fails to compile rather than answering false. What is kept is a
  documentation defect: the house rule tells a consumer to use the helper, the
  helper does not accept the value they hold, and nothing points at `.Unwrap()`
  (`crud/repo.go:101`, which this repository's own [[D-061]] tests use at
  `crud/basenext_test.go:53`).
