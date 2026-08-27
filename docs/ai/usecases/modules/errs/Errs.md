# errs · errs/sqlerr — one failed request, told to a client in words it can branch on

**Covers:** `github.com/frostgrove/vv/errs`, `github.com/frostgrove/vv/errs/sqlerr`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — the value types, the corpus behind the four dialect tables and the message ladder are the strongest part of this repository, and five things the tag makes **irreversible** are not: the retryable `error_code` is three different words on four engines with no decision covering it, `CodeExclusion` is advertised and produced by nothing so closing it later changes bytes a shipped client already parses, an SPI interface is frozen into the contract with no caller anywhere, the corpus JSON shape joins the compatibility surface with it, and nothing in the library ever writes down what a client is supposed to do with a code it has never seen. The edge pass adds configuration mistakes that the public values let through as legitimate responses: an empty code, an unknown kind and a negative array position all render instead of failing at wiring; `Fault` keeps nested parameter values live despite promising a deep copy; and reload atomicity and locale-case behaviour remain unverified where the code has no focused control. Wrapped driver errors are deliberately absent from `Fault.Error()`, JSON and the standard 500 envelope, but remain reachable through `errors.As` for caller-controlled, redacted diagnostics. `sqlerr` is conservative about a malformed tuple, but the public corpus tools accept internally inconsistent records and `Save` can leave the requested directory. Most are additive repairs, but the silent wrong answers should be closed before a tag.

## What a consumer is actually trying to do

Somebody is shipping a product with a signup form, an admin screen and a public
API. A write fails. What they want back is not a sentence — it is a machine word
their front end can switch on, and the name of the field the client itself sent,
so the form can put a red border in the right place. They want that word to be
the same word whether the refusal came from a unique index, from a validation
library, or from a rule they wrote themselves last Tuesday.

They want it to be one round trip. A form with a taken email, a missing
organisation and an under-age user is one bad payload, not three, and a client
that has to submit three times to learn three things is the thing they are trying
to stop doing.

They want the words to be theirs eventually. `unique` is fine on day one. By
month two the product has `email_taken`, `seat_limit_reached` and `too_young`,
each with its own text, each in three languages, and none of that should require
touching a status table or forking a renderer. They also want the reverse, on the
one form that faces the open internet: an answer that does **not** say the
address is already registered, because saying so tells anybody with a browser who
has an account.

They want the same answer on every server they run. They develop against a file,
their cloud runs PostgreSQL and two on-prem customers run MySQL. A client that
switches on the code is written once, against whatever the laptop answers, and it
has to keep being right in the other three places — including the mobile build
that shipped in March and cannot be redeployed when the service starts saying a
word that did not exist then.

Most of their failures will not be any of this. They will be a 500 nobody
classified, and a customer saying "it broke at about three". They want the
response to name nothing from inside — not the constraint, not the table, not
the SQLSTATE, not the driver's sentence — and they want every one of those in the
log line for that same request, with something in the body they can trade for it.

And they want the branch they already wrote to keep working, and a way to test
the ones they write next. There is an `errors.Is` in their service layer today,
and a `TestCreateRejectsAnUnderageUser` they are about to write. Adding any of
this must not be the change that quietly stops the first from matching, and the
second should not cost fifteen lines of hand-rolled comparison.

## Happy cases

### H-ERRS-01 — A taken email comes back as a word and a field
**Who:** the engineer who owns the signup endpoint of a B2B SaaS
**Wants:** the 409 to say `unique` at `email`, and to say nothing else
**Story:** Two people register with the same address. The second write is refused
by the unique index. The front end has to mark the email input and show a
sentence; the support engineer reading the log has to know which index it was.
**Must hold:**
1. The response carries a stable machine code the client can switch on, not a
   sentence to be parsed.
2. It names the field the client sent, not the Go field and not the index.
   *(Inherited: the column-to-field hop and the field-to-client-key hop are two
   other layers' — see *The error body names the key the client sent* in the
   `port` sweep. It is here because a 409 with no field lands in the `general`
   group and a form cannot mark anything, which is the whole job of the case.)*
3. No constraint name, table name, SQLSTATE or driver text is anywhere in the
   body.
4. Two identical failing requests produce a byte-identical body.
5. A `field` that was **guessed** is distinguishable from a `field` that is a
   fact, or the contract says plainly that it is not.
**Today:** 🟡 partial — 1, 3 and 4 hold everywhere; 2 needs a fourth wiring line
on PostgreSQL and a fifth on the other three; 5 does not hold and is not stated
either way
**Evidence:** 1: `errs/sqlerr/postgres.go:18` maps `23505` to `CodeUnique`;
`errs/codes.go:68` gives it `KindConflict`, which is the 409;
`TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames`
(`errs/sqlerr/classify_test.go:62`) pins it against captured servers.
3: `errs/violation.go:83` emits `field`, `error_code`, `message` and nothing
else, on a **value** receiver so a violation marshalled as a map entry or a
struct field cannot bypass it; `TestAMarshalledFaultNamesNothingInternal`
(`errs/marshal_test.go:125`) and `TestAViolationMarshalsOnlyFieldCodeAndMessage`
(`:174`).
4: `errs/violation.go:156` fixes the order and `errs/message.go:178` refuses to
range a map building a sentence ([[D-014]]).
2 costs wiring, and a different amount of it per engine. A PostgreSQL `23505`
names a constraint and a table and **no column** —
`TestOnlyPostgreSQLFillsInASource` (`errs/sqlerr/classify_test.go:439`) asserts
exactly that — so the columns come from
`sqlfault.WithColumns(sqlfault.FromCatalog(cat))`, and without them
`crud/decorators/faults/faults.go:142` returns before setting a path.
`errs/sqlerr/mysql.go:45-51` and `errs/sqlerr/sqlite.go:46-61` return
`errs.Source{}` on every arm, so on those three the only route to a field is
`faults.WithProbe(probe.Full(cat))`, which folds a path onto the driver's
violation **by code alone, and only when exactly one probe finding carries that
code** (`crud/probe/full.go:199-221`). [[D-019]] difference 11(c) states the
guarantee and its price in one sentence: *"The `field` is the same; the certainty
behind it is not."* What is wrong is the front page, not the mechanism:
`docs/modules/en/errs.md:71-99` shows the two-line wiring with no catalog and no
probe and a body carrying `"field":["email"]`, which that wiring cannot produce
on any engine.
5 is where the contract is silent. `Violation.Approximate` marks a path a
resolver chain declined (`errs/violation.go:66-70`, set at
`port/violations.go:85-86` and `crud/decorators/faults/faults.go:146-148`) and
`Violation.MarshalJSON` drops it on purpose (`errs/violation.go:78-82`). So a
form receiving `"field":["Email"]` — the Go field name, capitalised, because the
client-key hop declined — sees a response byte-shaped exactly like a correct one.
**If not ready:** On PostgreSQL, load a catalog at start-up and pass
`sqlfault.WithColumns(sqlfault.FromCatalog(cat))` — the usage guides show it
(`docs/usage-guides/gorm.md:1260`, `docs/usage-guides/ent.md:1341`) and the
module's own front page does not. On the other three, add the probe and accept
one extra statement per refusal. The cheap fix is documentation: correct
`docs/modules/en/errs.md:71-99` so the flagship example is one a reader can run,
and say on `errs.Violation` what a `field` is worth — either it is always exact
because the approximate ones are dropped, or a client must treat it as advisory.
Neither is written anywhere today.

### H-ERRS-02 — A rule the service wrote lands in the same list
**Who:** the same engineer, adding "you must be 18"
**Wants:** their own refusal to look identical to the database's, with their own
wording
**Story:** They write the check in the service layer, give it a code and a
message, and return it. The handler renders it without knowing whether a database
was involved.
**Must hold:**
1. Building the refusal is one expression, not a struct literal.
2. The status comes from the code's declared class, so declaring a new code costs
   no edit to a status table.
3. The text they wrote at the call site is the text the client reads.
**Today:** 🟡 partial
**Evidence:** 1 and 2 hold: `errs/build.go:33-62` is ten entry points and
`errs/build.go:114` sets the open violation's message; `errs/codes.go:120`
declares the code with its kind. 3 does not survive contact with a catalogue.
The ladder in `port/violations.go:117-130` is catalogue, then the violation's own
`Message`, then the vocabulary's default — and `errs.Messages` answers for **any**
declared code out of the vocabulary (`errs/message.go:103-107`), so the
violation's own text never gets its turn.
`.Field("Age").Code(errs.CodeCheck).Message("age must be at least 18")` renders
as written with no catalogue wired, and as *"this value is not allowed"* the
moment one is: same fault, two bodies, decided by an unrelated start-up call.
The `.Field(...)` is load-bearing in that sentence and the round-1 draft omitted
it: without an open violation, `Code` and `Message` land on the **fault**
(`errs/build.go:88-95`, `:114-122`, guarded on `b.open < 0`), which is a
different defect — blocker 15's.
**If not ready:** Give the rule a code of its own and put the text in the
vocabulary or the catalogue rather than at the call site. `Builder.Message` is
then for a violation with nowhere else to live, which is what its godoc already
says (`errs/build.go:111-113`) — the surprise is that the two paths differ at all,
not that the godoc promises otherwise. Watch the template: see H-ERRS-05
must-hold 4 before writing `"must be at least {min}"`.

### H-ERRS-03 — The validator's errors and the database's in one list
**Who:** an engineer who already uses go-playground/validator on request structs
**Wants:** the struct-tag failures and the constraint failures in one response
**Story:** They validate the request, and whatever the validator reports they
want carried into a fault so the handler renders one body. Later they add a
second source — a uniqueness pre-check — and expect to append to the same list.
**Must hold:**
1. Converting the validation library's errors costs no dependency and no adapter
   type.
2. The converted violations reach a fault in the same expression that classifies
   it.
3. Two sources of violations can be merged into one fault.
4. What comes out carries a code the vocabulary knows and a sentence a person can
   read.
**Today:** 🟡 partial
**Evidence:** 1 is done well — `errs/bridge.go:17` is satisfied structurally,
with the tag-name control test in `test/bridge/fieldviolation_test.go`.
2 and 3 have a supported form and it is a loop: `Violation` has seven fields
(`errs/violation.go:55-71`) and the builder has a step for each
(`errs/build.go:68,88,100,114,124,130,137`), so a `for _, v := range vs` rebuilds
any violation without writing through a returned fault. Seven chained calls per
violation, written by the consumer, is what "supported" means here. No step takes
a `[]Violation` and there is no `errs.Merge`; the module page's own example,
`docs/modules/en/errs.md:452-453`, ends in
`return errs.Validation().Fault()   // …carrying vs` — a snippet that does not
compile, because `vs` is never used.
4 is a **message** defect and not a status one, and the round-1 draft got this
wrong in a way that mis-sized it. `Tag()` becomes the `Code`
(`errs/bridge.go:58`), and `gte`, `email` and `oneof` are not in `StandardCodes`,
so `Codes.KindOf` declines. That contributes **no kind at all** rather than
`KindInternal`: `port.KindOfWith` seeds `worst := f.Kind` and only escalates on a
declared code (`port/kind.go:40-46`), with the godoc at `:28-31` saying it is
written that way precisely so *"a service that declared `too_young` and forgot to
wire it must not have its own 422 turned into a 500 by the omission"*. A fault
built by `errs.Validation()` still answers 422. What is wrong is the sentence:
`port/violations.go:129` returns the code as its own message, so the client reads
`{"error_code":"gte","message":"gte"}` with a correct status.
**If not ready:** Build the fault, then assign the field:
`f := errs.Validation().Wrapping(err).Fault(); f.Violations = vs; return f`.
Three lines and a write-through to a value `Builder.Fault` deep-copied on purpose
(`errs/build.go:192-215`) — the copy is on the way *out*, so a later write lands
inside the fault the renderer will read. **Do not reach for `port.FaultFrom`**
(`port/kind.go:250-275`), which is exported, takes a `[]errs.Violation` and looks
like the missing step: it is the wire-decode inverse and rebuilds only `Path`,
`Code`, `Message` and `Approximate`, dropping `Params` and `Source` and forcing
`Origin` from the kind. Dropping `Params` is exactly what empties the `{param}`
templates the preset in the DX section depends on. One `Builder.Violations([]Violation)`
closes 2 and 3 together. 4 needs a preset: see the DX section.

### H-ERRS-04 — The product's own vocabulary, without a status table
**Who:** a tech lead defining `quota_exceeded`, `seat_limit_reached`, `too_young`
**Wants:** to declare codes once and have every transport give them the right
status
**Story:** They build a vocabulary at start-up, add three codes with their
classes and default texts, and hand it to whatever needs it.
**Must hold:**
1. Declaring a code is one call, and a second declaration disagreeing about the
   class is refused rather than silently winning.
2. A consumer can hand two different `*errs.Codes` values to two subsystems in
   one process and each gets its own. *(The checkable form of "nothing global",
   which is [[D-016]]'s rationale rather than a guarantee a consumer can test.)*
3. The declared class is what the HTTP status and the gRPC code come from — on
   the routes this library generated, not only on hand-written ones.
4. A second declaration of a code that **agrees** about the class is visible
   somewhere, because the two declarations disagree about the sentence.
**Today:** 🟡 partial — 1 and 2 are exemplary, 3 costs one option and one trap,
4 is silent by design
**Evidence:** 1: `errs/codes.go:120` returns `ErrCodeRedeclared` on a disagreeing
kind and leaves the existing declaration alone
(`TestRedeclaringACodeWithADifferentKindIsRefused`, `errs/codes_test.go:24`).
2: it is a value, not a registry (`errs/codes.go:48`,
`TestANilCodesReadsAsEmptyInsteadOfPanicking`, `errs/codes_test.go:159`), and
`errs/doc.go:22-27` records why.
3 holds for a renderer a consumer builds themselves —
`TestAConsumersVocabularyDecidesTheStatusAndTheDefaultMessage` in
`port/porthttp/render_options_test.go` — and, contrary to round 1, it also
reaches `POST /users` with **one option**:

```go
crudfiber.New(repo, crudfiber.WithRenderer[User, int64, UserUpdate](
    crudhttp.NewRenderer(crudhttp.WithCodes(codes), crudhttp.WithMessages(cat))))
```

Nothing is lost there, and the round-1 claim that this is *"a rewrite, not an
option"* was wrong. `New(repo)` contributes **no** path hop to lose:
`port.Hops` reads `svc.Paths()` (`port/path.go:53-64`), which is nil unless
`port.WithPaths` was passed, and `Rules.Service()` emits only `WithQuery` and
`AllowClientID` (`port/rules.go:70-79`); `port.Identity[M]` implements `Model`
alone (`port/mapper.go:21-25`) and is not an `errs.Resolver`. The raw-body index
and the request locale are not `RenderOption`s at all — the binding installs them
per request for whichever renderer produced the failure
(`crud/http/crudfiber/options.go:192-204`, `port/porthttp/render.go:135`).
The real trap is narrower and sharper: on `NewFor(repo, mapper)` with a generated
`<Model>Mapper`, and on `Serving(svc)` where the service declared `WithPaths`,
`WithRenderer` **discards that hop silently** —
`crud/http/crudfiber/handler.go:118-124` calls `rendererFor(port.Hops(svc, mapper))`
only when no renderer was supplied, and `rendererFor` passes `WithResolvers` and
nothing else (`crud/http/crudfiber/options.go:150-155`, and the identical lines
in `crudnet`, `crudgin` and `crudgrpc`). The consumer gets their vocabulary and
loses their field names, with no error and nothing to grep for.
The other loud wrong turn is `crudfiber.Errors(crudhttp.WithCodes(codes))`. It
compiles (`crud/http/crudfiber/middleware.go:21` takes `...crudhttp.RenderOption`),
it is the obvious thing to write, and it is documented as being *"for handlers
this library did not write"* (`:10-11`) — so it applies to no route this library
mounted.
4 is `errs/codes.go:114-115` in as many words: *"Declaring the same code twice
with the same kind is allowed and the first message wins."* Two libraries — or a
preset plus the application — both declaring `too_long` means whichever ran first
decides the sentence every client reads, with no error and nothing to grep for.
**If not ready:** Three separate seams have to be handed the same value — the
renderer, the classifier (`sqlfault.WithCodes`) and the catalogue — and nothing
detects a process that wired three different vocabularies into the three. For a
resource with a declared hop the honest answer today is to rebuild the hop by
hand: `crudhttp.WithResolvers(mapper)` beside the vocabulary, using the value
already typed on the same line. That works, and nothing tells the consumer they
had to. See the DX section for the two-part ask: an option that extends the
computed hops, and a wiring-time panic where a supplied renderer would drop one.

### H-ERRS-05 — The same failure, in Russian, with one field overridden
**Who:** an engineer localising an existing API
**Wants:** per-locale wording, plus one narrower sentence for one field
**Story:** They add `messages/default.json` and `messages/ru.json`, embed them,
and load them at start-up. For the signup form they want *"somebody already
signed up with that address"* rather than the generic taken-value text — but only
there.
**Must hold:**
1. Adding a locale is a file, not a package.
2. An override can be as narrow as one field and one code, or as broad as one
   code, with no schema to learn.
3. A locale with three entries and a vocabulary with forty codes is normal, not
   broken.
4. A template whose parameter is missing does not put `{max}` in a response body.
5. A locale file can be reloaded while the server is serving traffic and `-race`
   stays silent.
6. If reloading is start-up-only, every place that documents `Load` says so.
7. A language whose plurals differ by count can express them.
**Today:** 🟡 partial — 1, 2 and 3 are the best-designed thing in the module; 4
holds in one of the two places it has to; 5, 6 and 7 do not
**Evidence:** 1: `errs/catalogue.go:36` loads a directory of flat JSON files.
2 and 3: `errs/message.go:116-150` walks `user.email.unique → user.unique →
email.unique → unique` then the code's own default, pinned by
`TestEachLevelOfTheMessageLadderResolves` (`errs/message_test.go:20`) and
`TestTwoLocalesThroughTheSameFaultGiveTwoMessages` (`:140`); the locale reaches
the ladder from the context, so the fault never carries the request's language.
4 is `TestATemplateWithAMissingParamFallsBackRatherThanEmittingThePlaceholder`
(`errs/message_test.go:67`), and `expand` is called from `errs/message.go:98,104`
**and nowhere else**. The rung below it, `port/violations.go:126`, calls
`errs/codes.go:145` `MessageFor`, which returns the declared template raw. The
round-1 draft scoped this to *"a process that wired no catalogue"* and that is
too narrow, which matters because it makes the leak look like something wiring a
catalogue avoids. `Messages.Message` expands the vocabulary default **itself**
and answers `("", false)` when it cannot (`errs/message.go:103-107`), so
`port.message` then falls past the catalogue rung, past the violation's own text,
to `defaultMessage` — which hands back `{max}` unexpanded. A fully catalogued
process ships the placeholder for any key its catalogue does not cover. That is
the exact shape `docs/modules/en/errs.md:394` teaches
(`"too_long": "at most {max} characters"`), and no library-produced violation
ever supplies `{max}`: the only things that set `Params` are the bridge
(`errs/bridge.go:62,68` — `param` and `value`) and the probe's opt-in
`WithValues` (`crud/probe/full.go:261`, `value`).
5: `errs.Messages` and `errs.Codes` hold plain maps and `grep -n "sync\." errs/*.go`
is empty. `Message`, `KindOf` and `MessageFor` are read from every request
goroutine through `port.Violations`. 6: `docs/modules/en/errs.md:421` says
*"`Messages.Load(fsys, dir)` adds a locale at run time"*. A consumer who follows
that sentence behind a running server has a data race in the one package every
request touches, and no test can see it because no test loads while rendering.
7: `expand` is `{name}` substitution and nothing else (`errs/message.go:178+`),
and a locale file is one flat key→string map (`errs/catalogue.go:109-130`).
Russian needs three forms for *"{max} characters"* and there is no plural
mechanism and no per-key escape.
**If not ready:** For 4, declare defaults without placeholders and put every
templated sentence in the catalogue, where `expand` runs — or move the guard down
into `port.defaultMessage`, which is three lines and is the fix that also covers
the catalogued process. For 5 and 6, either the sentence at `errs.md:421` says
start-up only, or `Messages` grows an `RWMutex`. Nothing else in this module is
one sentence of documentation away from the failure `-race` exists to catch.
For 7 the answer is good and unwritten: `MessageSource` is one of the five frozen
SPI interfaces (`errs/spi.go:38-40`), so a consumer replaces `errs.Messages`
wholesale with go-i18n or ICU and keeps everything above it. That reframes
must-hold 1 as the trap it is — *adding a locale is a file* is true until the
second language needs plurals, at which point the whole catalogue is thrown away
and the loader that read it was never the point. Neither `errs/doc.go` nor
`docs/modules/en/errs.md` says so.

### H-ERRS-06 — A half-translated catalogue fails the build
**Who:** whoever owns CI
**Wants:** a test that goes red when a new code ships without Russian text
**Story:** They add one test that asks the catalogue which declared codes the
`ru` locale does not cover, and fails if the list is not empty.
**Must hold:**
1. There is a way to ask, by locale, and it is a report rather than a start-up
   refusal.
2. A code that `en-GB` inherits from `en` does not count as missing.
3. A code that has English text and no Russian is reported for `ru`.
**Today:** 🟡 partial — 1 and 2 hold, 3 does not
**Evidence:** `errs/catalogue.go:160-180` returns a sorted `[]Code`, and walks
the locale ladder before deciding — `TestMissingNamesTheCodesWithNoTemplate`
(`errs/catalogue_test.go:157`). The ladder is `locales(locale)`
(`errs/message.go:159-170`), whose last rung is `""`, the **default** file. So a
code whose only template lives in `default.json` — the setup H-ERRS-05 describes
one case earlier — is reported as covered for `ru`. What `Missing` catches is a
code with no template in any file at all. No **`Missing`** fixture pairs a
`default.json` with a second locale: the two ladder subtests at
`errs/catalogue_test.go:157-198` use `default.json` alone and then
`en.json`/`en-GB`/`fr`. (Other fixtures in the same file do pair them —
`:205-207`, `:219-221` — which is why the round-1 wording *"no fixture pairs
`default.json` with a second locale"* was wrong and would have cost a reader ten
seconds and the paragraph's credibility.)
**If not ready:** The test a consumer actually needs — which keys does `ru`
declare, against the vocabulary — cannot be computed: `m.templates`
(`errs/message.go:47`) is unexported and `Locales()` returns names only. Either
`Missing` takes a flag that stops before the default rung, or `Messages` exports
a reader for one locale's declared keys. Separately, it reports the
**vocabulary's** codes, so every bridged validator tag (H-ERRS-03 must-hold 4) is
invisible to the check — a different defect with the same fix, since a tag that
is not declared cannot be reported missing. `errs.ValidatorCodes` in the DX
section closes that half; the default rung needs its own change.

### H-ERRS-07 — Everything in the log, nothing on the wire
**Who:** the on-call engineer at 03:00
**Wants:** the constraint, the SQLSTATE and the driver's own error in the log
line for the request the client saw as a bare 409
**Story:** They catch the error at the edge, log it, and render it. The log line
should be enough to find the index; the body should still name nothing.
**Must hold:**
1. The internal half is reachable from the error, in one place.
2. The obvious ways of printing an error cannot leak it.
3. Getting a full diagnostic into a structured log line is one call, not a
   field-by-field format string.
**Today:** 🟡 partial
**Evidence:** 1 and 2 are solid — `errs/fault.go:42-51` holds it all, and
`Fault.Error` (`:68`), `Fault.String` (`:155`) and `Fault.MarshalJSON` (`:114`) all
deliberately drop it, with `TestAPrintedFaultNamesNothingInternal`
(`errs/marshal_test.go:330`) and `TestAFaultsErrorTextCarriesNothingInternal`
(`errs/fault_test.go:141`) as the guards, each carrying its own control
([[D-047]] Proven-by). 3 does not exist. There is no `LogValue`, no `Dump`, no
debug renderer. `errs.Detail` has neither `String()` nor `MarshalJSON` — there is
no method on `Detail` or `Source` anywhere in the package — so
`slog.Any("detail", f.Detail)` is the one obvious print that does spill the
constraint and the driver's sentence. The leak is one level down from the type
everybody guarded.
The comment at `errs/fault.go:63-67` says `porthttp` *"copies the outermost
err.Error() into the body of every status below 500"* and that *"the exported
fields and `Fault.MarshalJSON` are the debug channel"*. Both halves are false
today: the envelope replaced that body (`port/porthttp/render.go:123-141`), and
`MarshalJSON`'s own doc forty lines below says `Detail`, `Source`, `Message` and
the wrapped errors stay behind. [[D-047]]'s **What it forbids** repeats the
second sentence verbatim (`:57-58`) and must be corrected with it. Its
present-tense first half is `errs/fault.go`'s alone — the decision did anticipate
the envelope, at `:60-62`: *"The envelope replacing `Body` removes one path from
`Error()` to a client, not every path."*
**If not ready:** The consumer writes their own dumper over `f.Detail.Dialect`,
`f.Detail.SQLState`, `f.Detail.Constraint`, `f.Detail.Table` and
`errors.Join(f.Unwrap()...)` — about eight lines, five field names, in every
service — and gets a string their aggregator has to regex apart.

### H-ERRS-08 — A job fleet retries only what is worth retrying
**Who:** whoever owns the nightly billing workers
**Wants:** to retry deadlocks and serialisation failures and nothing else
**Story:** A worker wraps each unit of work. On a deadlock it backs off and runs
again; on a constraint violation it dead-letters the job, because running it again
will fail the same way.
**Must hold:**
1. A lock failure and a constraint failure are distinguishable without reading
   any message text.
2. It works on all four engines, including the one that reports no SQLSTATE.
3. A retryable answer can never also be a conflict.
4. The metrics the worker emits per code mean the same thing on every engine.
**Today:** 🟡 partial — 1, 2 and 3 are exemplary; 4 is the same defect
H-ERRS-09 grades a blocker, and this case does not get to call it a caveat
**Evidence:** `errs/sqlerr/postgres.go:25-28`, `errs/sqlerr/mysql.go:30,33`,
`errs/sqlerr/mariadb.go:27,30` and `errs/sqlerr/sqlite.go:57-58` cover all four;
`errs/codes.go:94-98` puts every one of them in `KindRetryable`
(`TestTheRetryableCodesAreTheirOwnKind`, `errs/codes_test.go:71`).
`TestARetryableCaseNeverAnswersAConflictOrValidationCode`
(`errs/sqlerr/classify_test.go:394`) is 3, tested against the captured corpus
rather than asserted. `errs/fault.go:38-41` records why there is no separate
`Retryable` field: a second spelling would make representable the one state
[[D-040]] forbids.
4 fails on three engines out of four. One provoked serialisation failure
classifies as three different public codes: `serialization_failure` on
PostgreSQL, `deadlock` on MySQL and MariaDB, `lock_timeout` on SQLite — the
`want` values in `errs/sqlerr/testdata/corpus/*.json` for the case named
`serialization_failure`, from `errs/sqlerr/mysql.go:33` and
`errs/sqlerr/mariadb.go:30` mapping `{"40001", 1213}` to `errs.CodeDeadlock`
under the comment *"deadlock, serialization_failure"*, and
`errs/sqlerr/sqlite.go:57-58` answering `CodeLockTimeout` for the whole busy
family. The provoked `deadlock` case is `lock_timeout` on SQLite too. None of
those is one of the four coarsenings — `coarsened`
(`errs/sqlerr/classify_test.go:48-53`) folds only `primary_key`, `not_null`,
`missing_default` and `bad_type` — so this is not a designed collapse, it is four
tables that disagree.
**If not ready:** The branch itself is
`f, ok := errs.AsFault(err); ok && f.Kind == errs.KindRetryable`, or
`port.KindOf(err)` for one line; there is no `errs.IsRetryable(err)`, and
`grep -rn "IsRetryable" --include='*.go' .` is empty. The **kind** is the same on
all four engines and it is what the branch reads, so the retry decision is safe.
The histogram is not, and the fix is H-ERRS-09's.

### H-ERRS-09 — We develop on SQLite and ship on PostgreSQL
**Who:** the engineer who wrote the client's error switch on their laptop
**Wants:** the same payload refused the same way, with the same word, wherever it
runs
**Story:** Tests run against a file. Staging and production are PostgreSQL. Two
on-prem customers run MySQL. They write `switch (err.error_code)` once, against
whatever the laptop answered, and ship it to all three. Two of their writes are
in one `InTx`.
**Must hold:**
1. A payload refused on one engine is refused on the others.
2. When it is refused, the `error_code` is the same word — or the difference is
   on a list somebody chose.
3. The body has the same shape — a `field` where the payload named one.
4. The **status** does not change per engine for the same sequence of statements.
**Today:** ❌ missing on all four, and only two of the four are written down
where a reader would find them
**Evidence:** 1: SQLite enforces neither width, nor range, nor declared type, so
`too_long`, `out_of_range` and `bad_type` are **200 on SQLite and 422 on the two
servers**. The corpus records it as the absence of an expectation — those three
cases carry an empty `want` in `errs/sqlerr/testdata/corpus/sqlite.json` and a
code in the other three files. [[D-019]] difference 6 states it. ✅ documented.
2 has two families and they are not the same finding.
The **`restrict` split** — `restrict` on MySQL and MariaDB, `foreign_key` on
PostgreSQL and SQLite — *was* chosen, deliberately, and the round-1 draft's
*"a difference nobody chose"* was wrong. [[D-046]] settles it in its **What it
forbids**: *"the corpus records the collapse rather than papering over it …
MySQL and MariaDB do separate the two in the key, 1451 against 1452 … That row is
not what this forbids."* `docs/modules/en/sqlerr.md:105-113` repeats it. Chosen,
explained, documented in the right place.
The **retryable family** was not. `serialization_failure` answers three different
words on four engines and `deadlock` answers two (H-ERRS-08's evidence), and no
decision, no module page and no test covers it. `TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames`
reads each engine's own `want`, so nothing compares the four; and
`TestEveryEngineAnswersTheSameQuestions` (`errs/sqlerr/classify_test.go:515-538`)
— the test whose name promises exactly this check, and the one a reader will grep
for and stop at — compares only `Cases[i].Name` and never `Want`. [[D-019]] says
*"There are eleven"* observable differences and lists none about the code a
client branches on.
3: see H-ERRS-01 — [[D-019]] difference 11(c) does say so.
4 is the one nobody has written down anywhere, and it is the one that changes the
HTTP status rather than a word. On PostgreSQL every statement after a failed one
in the same transaction returns `25P02` → `transaction_aborted`
(`errs/sqlerr/postgres.go:28`) → `KindRetryable` (`errs/codes.go:97`) → **503**.
On the other three the statement rolls back alone and the transaction stays
usable — the corpus says so in its own words, `"unreachable": "the statement
rolls back and the transaction stays usable; only PostgreSQL poisons the whole of
it"`. So a consumer who develops on SQLite, ships on PostgreSQL and wraps two
writes in one `InTx` hands a well-behaved client a 503 for a transaction that is
permanently poisoned, and the client retries. Forever. `errs/code.go:52-58`'s own
comment is about exactly this case and stops at "the caller cannot tell it from a
bug". [[D-019]] difference 11(b) covers the probe's side of the same fact and not
the status.
**If not ready:** For 1 there is no workaround short of a Go-side length check,
which [[D-042]] refuses for its own reasons — the honest answer is "run the
engine you ship on in CI", and it should be a sentence in the module page rather
than something a reader assembles from three files. For 2 the fix is a decision:
either the retryable words are one word on all four engines (losing a
distinction PostgreSQL can make), or the difference joins [[D-019]]'s list as a
twelfth and the module page says which clients must handle all three. For 4 the
same list needs a thirteenth row, and it is the one that needs a sentence in
`docs/modules/en/sqlerr.md` most: *do not retry `transaction_aborted` inside the
transaction that produced it.* All three of these are cheaper as a decision than
as code, and all three are wire-visible after the tag.

### H-ERRS-10 — `unique` becomes `email_taken`, on one constraint
**Who:** an API owner whose public contract has product words in it
**Wants:** the response to say `email_taken` where the classifier said `unique`,
without changing what any other unique index answers
**Story:** They keep a small table from what the classifier found to their own
code, hand it to the renderer at start-up, and the envelope carries their word.
**Must hold:**
1. There is a declared seam for it, and wiring it is one option at start-up.
2. The portable key is the code and the path, which every engine supplies. The
   constraint may be a key too, on the engine that has one — and it must not
   thereby reach the wire.
3. The overridden code still decides the status through the vocabulary, so a new
   word needs no status-table edit.
**Today:** ❌ missing
**Evidence:** `errs/spi.go:29-31` declares `CodeMapper`, `errs/doc.go:51` freezes
it into the contract at the first tag, and `docs/modules/en/errs.md:437`
documents it as *"implemented by a service that wants `email_taken` where the
classifier said `unique`"*. Nothing in the repository calls `CodeFor`:
`grep -rn "CodeFor(" --include='*.go' .` finds `crudgrpc.CodeFor`
(`crud/rpc/crudgrpc/status.go:74`), which is the unrelated kind→gRPC-status
table, and the declaration. There is no `WithCodeMapper` on
`porthttp.NewRenderer` (`port/porthttp/render.go:46-87`), none on the gRPC
renderer, and none on `sqlfault.New` (`crud/sqlfault/classify.go:29-44`). No test
names it. It is the only one of the five frozen SPI interfaces in that state:
`Classifier` is `crud/sqlfault/classify.go:71`, `Resolver` is `port/path.go:29`
and `port/porthttp/bodyindex.go:85`, `MessageSource` is `errs/message.go:86`,
`FieldViolation` is `errs/bridge.go:17` with `test/bridge/fieldviolation_test.go`.
`CodeFor` receives the whole violation including its `Path` (`errs/spi.go:30`),
so must-hold 2's portable key works on all four engines the day it is wired;
constraint-keying is the PostgreSQL affordance on top.
**If not ready:** The consumer wraps the repository or the handler, calls
`errs.AsFault`, rebuilds the whole fault with a second builder chain, and
re-wraps the sentinels by hand — twenty-odd lines that duplicate `Builder.Fault`
badly, because `Wrapping` is the only door to `Unwrap` (`errs/build.go:175-182`)
and there is no way to copy a fault's wrapped errors across. Two independently
shippable halves, and they should be priced separately: **(a)** the option on
`port/porthttp` and `crud/rpc/crudgrpc` plus the call inside `port.Violations` —
usable on day one through `WithRenderer`, so it ships before the tag and closes
H-ERRS-16 too; **(b)** the option that keeps a declared hop, which is H-ERRS-04's
ask and can follow in a point release. The irreversible half is neither: it is
that the tag freezes an interface signature nobody has called once.

### H-ERRS-11 — A classifier for a store this library never saw
**Who:** a team on DynamoDB, or on an ORM whose errors are its own
**Wants:** their failures to arrive as the same faults, so the same handler
renders them
**Story:** They implement `Classifier` over their driver's errors, wire it where
`sqlfault`'s would go, and expect the rest — status, ladder, envelope — to work
unchanged.
**Must hold:**
1. Implementing it costs no import from this library beyond `errs`, and the
   contract does not presuppose a SQLSTATE.
2. What they build reaches a client with the same status, order and shape as a
   fault this library built.
3. The `errors.Is` branches in the service layer above keep firing.
4. `sqlerr.Classify` is usable on its own by a team that wants only the SQL
   tables and none of the repository.
**Today:** 🟡 partial — 1, 2 and 4 hold, 3 does not and cannot
**Evidence:** 1: `errs/spi.go:5-16` says it in the godoc — *"An implementation
must not presuppose that a SQLSTATE exists. SQLite reports none at all and never
will"* ([[D-046]]). 4: `errs/sqlerr/classify.go:28` is a pure total function over
a flattened `*sqlerr.Err`; `TestAnUnknownDialectAndANilErrorAreRefusedRatherThanPanicking`
(`errs/sqlerr/classify_test.go:492`) is the refusal,
`TestAParserAnswersTheSameWhateverTheServerSaid` (`:176`) and
`TestTheSameViolationInAnotherLocaleClassifiesIdentically` (`:290`) pin it
against a duplicate key captured from a server answering in Russian, and
`TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser`
(`errs/sqlerr/dialect_test.go:27`) is the control that keeps the tables honest.
3 is the asymmetry, and it is a design consequence rather than a bug:
`Fault.wrapped` is unexported and `Builder.Wrapping` is the only door
(`errs/build.go:175-182`, `errs/spi.go:12-14`), so a third-party classifier that
does not deliberately wrap `crud.ErrConflict` produces a fault matching **no**
sentinel — and every `errors.Is` branch in the service layer above goes quiet,
which is the exact guarantee H-ERRS-12 certifies as ✅ for this library's own
classifier. Nothing says so where an implementer reads.
**If not ready:** The implementer wraps the right `crud` sentinel by hand for
every kind they produce, and there is no list of which sentinel goes with which
kind outside `port.sentinelFor` (`port/kind.go`, unexported). Two things belong
in `errs/spi.go`'s `Classifier` godoc: that wrapping is not optional if the
consumer's own service layer branches on sentinels, and where the mapping lives.
Separately and packaging-shaped: `errs/sqlerr` exports `Path`, `Save`, `Load`,
`Corpus` and `Case` (`docs/api/surface.md:538-547`) beside `Classify` and `Err`.
Those five are capture tooling for `make corpus`, and `errs/sqlerr` is in the
contract manifest ([[D-048]], `scripts/checks.sh:TIER0`), so the tag freezes the testdata
JSON shape as a compatibility surface. Either that is deliberate and the doc says
so, or they belong in an internal package. That is irreversible at the tag and
free before it.

### H-ERRS-12 — The branch they already wrote keeps matching
**Who:** the engineer who has `errors.Is(err, crud.ErrConflict)` in a service
layer written before any of this existed
**Wants:** to wire classification, faults and a catalogue and change nothing else
**Story:** They add the classifier to the datasource and the enricher to the
repository. The service layer above is untouched, and its retry-or-report branch
has to keep firing on the same failures it fired on yesterday.
**Must hold:**
1. A classified conflict still matches the sentinel.
2. It keeps matching through however many further wrappings a service layer adds.
3. A fault that wraps nothing matches nothing — the guarantee is additive, not a
   blanket yes.
**Today:** ✅ ready
**Evidence:** the sentinel goes **inside** the fault rather than around it
(`crud/sqlfault/classify.go:110-115`), so the fault stays the outermost error and
`Fault.Unwrap` returns a list `errors.Is` walks (`errs/fault.go:101-104`).
`TestAFaultWrappingASentinelMatchesIt` (`errs/fault_test.go:43`) is 1,
`TestAFaultSurvivesBeingWrappedAgain` (`:91`) is 2, and
`TestAFaultWrappingNothingMatchesNothing` (`:62`) is the control that makes 1
evidence rather than a statement about `errors.Is`. [[D-038]] is the decision.
The enricher's copy carries the list by being a copy, with the reason written
where somebody would otherwise drop it
(`crud/decorators/faults/faults.go:126-130`).
**Kept as a case rather than folded into a "what already holds" list**, because
must-hold 3 is what H-ERRS-11 must-hold 3 fails at, and a reader who sees only
the green tick here would conclude sentinel matching is a property of faults
rather than of `Wrapping`.

### H-ERRS-13 — Five hundred rows in, thirty refused, one body
**Who:** the engineer behind a CSV import endpoint
**Wants:** one response listing what was wrong and which row it was wrong in
**Story:** The client posts an array. Thirty rows collide with existing emails,
three of them with each other. The importer's UI highlights the offending cells.
**Must hold:**
1. Each violation names its row and its field, not just its field.
2. A per-locale message for a code resolves the same for row 3 as for row 0.
3. Incompleteness is expressible on the fault itself, not only in the transport
   that renders it.
**Today:** ✅ ready for what is in remit
**Evidence:** 1: `errs.Path` carries index steps and renders them as numbers
(`errs/path.go:43-62`); `errs.ParsePath("Items[3].Email")` produces one
(`TestAnIndexedNamespaceBecomesAnIndexStep`, `errs/bridge_test.go:81`), and the
probe prefixes the row index onto whatever hop the caller resolved
(`crud/probe/full.go:267-284`, which is [[D-043]] applied to a batch).
2: `TestAnIndexedPathResolvesTheSameMessageAsAnyOtherRow`
(`errs/message_test.go:182`) — an index step is skipped when the ladder key is
built (`errs/message.go:123-124`), so one catalogue entry covers every row.
3: `Fault.Partial` (`errs/fault.go:49`) is set by the producers
(`crud/decorators/faults/faults.go:116`, `crud/probe/full.go:186`) as well as by
the cap, which is the right home: a fault that crossed a queue still knows its
list was cut.
**If not ready:** The cap itself — `port.MaxViolations`, `EnvelopeRenderer.max`,
`Envelope.Partial`, and the fact that `100` is a default nobody is told about —
is **`port` / `port/porthttp`'s**, and the round-1 draft spent this case's
evidence there. It belongs in that sweep. What stays here: `errs/doc.go` should
say what `Fault.Partial` obliges a client to do, since `errs` is where the flag
is declared and the module page a consumer reads.

### H-ERRS-14 — A gateway reads a downstream refusal and keeps its own branch
**Who:** the engineer who owns the BFF in front of three services, and whoever
writes the API tests
**Wants:** to decode one of these bodies back into something they can switch on
**Story:** The BFF calls the users service, gets a 409, and either forwards the
body or maps it onto its own. The test suite asserts `error_code` on a response
it just received.
**Must hold:**
1. The type a consumer reaches for first does not silently return zero values.
**Today:** ❌ missing
**Evidence:** `errs.Violation` has no struct tags and no `UnmarshalJSON`
(`errs/violation.go:55-71`), so `json.Unmarshal` into an `Envelope` yields
violations with no `field` and no `error_code` — even though `errs.Path` **does**
decode (`errs/path.go:76`), which makes the half-working shape more convincing
than a total failure would be. `port/porthttp/decode.go:88-95` says the omission
is deliberate and gives the reason: the decode is lossy and has to look it. That
reasoning is right. What is wrong is that nothing on `errs.Violation` or
`errs.Fault` points at the reader, so the first thing a consumer tries is the
thing that fails quietly.
**If not ready:** Two lines of godoc — a `[porthttp.ParseEnvelope]` pointer on
`errs.Violation` and on `errs.Fault` saying a violation is marshalled here and
decoded there, and that the decode is deliberately lossy. *(Round 1 carried two
further must-holds here and conceded in its own evidence that both were `port`'s.
They are: `porthttp.ParseEnvelope` has no test anywhere —
`grep -rn "ParseEnvelope" --include='*_test.go' .` is empty — and `Groups`'
`omitempty` plus the message-less 500 shape. Both belong to the **port /
port/porthttp** sweep, which owns the envelope, and are cut to this pointer.)*
What does belong here is the trap underneath them: `Fault.Message`'s godoc reads
*"developer-facing. never rendered"* (`errs/fault.go:45`) and
`port/violations.go:69` puts it into the violation it synthesises for a fault
that carries none. Every fault this library builds is clean — `port.FaultOf`
synthesises one that already carries a violation, and `auth.Unauthenticated` puts
its reason in a wrapped error (`auth/errors.go:33-38`) — so [[UC-015]] guarantee
11 still holds. What leaks is a fault a consumer built after reading the godoc.
[[D-056]] records the trap and leaves the comment standing.

### H-ERRS-15 — A customer says it broke at about three
**Who:** the support engineer, and the on-call who gets escalated to
**Wants:** to turn one customer's complaint into one log line
**Story:** A write fails for a reason no parser matched. The client gets a 500.
The customer opens a ticket with a screenshot, a rough time and the word
"error". Somebody has to find the request.
**Must hold:**
1. The 500 body carries something the customer can quote and the engineer can
   grep for.
2. The failure reaches a log somewhere without the consumer wiring anything, or
   the documentation says plainly that logging it is theirs.
3. Whatever handle the body carries is the same handle the log line carries.
**Today:** ❌ missing, and it is the most common interaction anybody will have
with this contract
**Evidence:** 1: a 500 is short-circuited before anything is copied out of the
fault (`port/porthttp/render.go:129-131`) and answered with `Internal()`
(`port/porthttp/envelope.go:37-49`) — one violation carrying `Code` and nothing
else, and `errs/violation.go:100-108` omits the `message` key entirely when the
message is empty. So the whole body is
`{"type":"error","errors":{"general":[{"error_code":"internal"}]}}`. There is no
request id, no correlation token, nothing. That is deliberate ([[D-015]]:
*"there is nowhere in this value for a driver's sentence to go"*) and it is the
right call for the *message*; it is not a reason for the body to have no handle.
`errs.Fault` has no field for one either (`errs/fault.go:42-51`).
2: nothing in the library logs a fault. `port.Logger(ctx)` is the seam
(`port/log.go:26`) and every caller of it in the tree logs a panic or a failed
encode — `crud/http/crudfiber/middleware.go:33`,
`crud/http/crudfiber/options.go:171`, `crud/rpc/crudgrpc/status.go:264` and their
twins — and no renderer calls it at all. A consumer who wires nothing gets a 500
that is invisible on both ends.
3 cannot hold while 1 and 2 do not.
**If not ready:** The consumer writes the middleware: catch, `errs.AsFault`,
generate an id, put it in a header and in the log line, and add it to the body
themselves — which means replacing `Internal()`, which means a `Renderer` of
their own, which is H-ERRS-04's seam again. Nothing in `docs/modules/en/errs.md`
or `errs/doc.go` says this is the consumer's job, and every service I would ship
needs it in week one. The smallest honest answer before the tag is a paragraph
saying so and naming `port.WithLogger` as where the log line goes; the better one
is `Fault.LogValue` (see the DX section) so that paragraph is one line of code
long.

### H-ERRS-16 — The public form must not confirm the address is registered
**Who:** an engineer whose signup form is unauthenticated and whose security
review is next month
**Wants:** the public route to answer `invalid_format` or a generic refusal at
`email`, and the admin route to keep answering `unique`
**Story:** They read H-ERRS-01, ship it, and a pen tester enumerates their whole
customer list by posting addresses at the signup form and watching for 409.
**Must hold:**
1. Which code a violation carries can be decided per deployment or per route,
   without forking the renderer.
2. Turning it off costs no loss of the field name, so the form still marks the
   right input.
3. What the log says is unchanged.
**Today:** ❌ missing — and this is the flagship case's own behaviour, seen from
the other side
**Evidence:** `unique` at `email` from an unauthenticated caller **is** account
enumeration, and the code-plus-path pair is sufficient on its own — no value is
needed. [[D-044]] closes the neighbouring door and not this one: its argument at
`:41-46` is about echoing the offending **value** (*"the 409 body confirms a row
the caller cannot read … that is [[D-008]]'s argument about 404 preceding 403"*),
and echoing is opt-in and off. Nothing anywhere weighs the pair. The only
declared seam for rewriting a code is `CodeMapper`, which is H-ERRS-10's blocker:
frozen into the contract, called from no line in the repository. So the answer to
a compliance requirement is an interface wired to nothing, and that is what
raises H-ERRS-10 from *an SPI with no caller* to *a requirement with no answer*.
**If not ready:** The consumer wraps the handler for the public route, decodes
the fault, rebuilds it with a second builder chain and loses the wrapped
sentinels doing it (H-ERRS-10's twenty lines) — or accepts the disclosure. There
is a third option nobody has written down and it is worse: return 200 from the
public route and lose the field. Wiring `CodeMapper` closes this case with the
same change that closes H-ERRS-10, and the module page should carry the example,
because the flagship example as it stands teaches the disclosure.

### H-ERRS-17 — A client shipped in March meets a code added in June
**Who:** the mobile engineer whose app is in an app store and cannot be recalled
**Wants:** to write one `switch` that keeps working when the service learns new
words
**Story:** They ship a build handling `unique`, `required` and `check`. In June
the service declares `email_taken`; in July `errs/sqlerr` grows a `23P01` arm and
starts answering `exclusion` where it answered `conflict`. The March build is
still on phones.
**Must hold:**
1. There is a written contract saying what to do with an unrecognised code, and
   it names the fallback.
2. The fallback is something every transport actually carries.
3. Adding a code to the vocabulary is not, by itself, a breaking change.
**Today:** ❌ missing — the whole Code/Kind split exists for this and nothing
states it
**Evidence:** The kind is **not on the wire**. `Envelope` carries `type`,
`partial` and `errors` (`port/porthttp/envelope.go:19-25`) and
`Violation.MarshalJSON` emits `field`, `error_code`, `message`
(`errs/violation.go:83-110`). So a client's only fallback for an unknown code is
the HTTP status — the gRPC code for a `crudgrpc` consumer — and neither
`errs/doc.go` nor `docs/modules/en/errs.md` says "branch on the code, default to
the status". 3 is where the absence bites: `errs.CodeExclusion` is declared,
kind-mapped and given a sentence (`errs/code.go:21`, `errs/codes.go:73`), and no
engine produces it — `23P01` has no arm in `errs/sqlerr/postgres.go:17-27`, while
`crud/sqlfault/gate.go:66-68` matches the SQLSTATE prefix `"23"`, so an exclusion
violation attaches `crud.ErrConflict` and renders as a bare 409 `conflict`
today. `docs/roadmaps/Roadmap.md:102-115` records both halves as deliberate. The
day somebody provokes an `EXCLUDE` and captures it, every March client handling
`conflict` starts receiving `exclusion` — and whether that is a breaking change
depends entirely on a contract nobody wrote.
**If not ready:** Nothing to write by hand; the consumer guesses. One paragraph
in `errs/doc.go` and one in the module page — *a client branches on `error_code`
and falls back to the status; the set of codes grows in a minor release; a code
never changes its kind* — would make `exclusion` shippable after the tag instead
of frozen out by it, and would make the H-ERRS-09 divergences survivable rather
than fatal ("handle all three words" is a contract; "the word differs and nobody
said" is not).

### H-ERRS-18 — An engineer tests their own service's refusals
**Who:** whoever writes `TestCreateRejectsAnUnderageUser`
**Wants:** to assert that a call produced a specific kind, code and path
**Story:** They call the service with a bad payload in a table test and compare
what came back against what they expected, the way they compare everything else.
**Must hold:**
1. Two faults built the same way compare equal by the obvious means.
2. Asserting one violation at one path is one line, not a loop.
3. Whatever helper exists is in the same place as the rest of the testing
   support a consumer already uses.
**Today:** ❌ missing
**Evidence:** `reflect.DeepEqual` on two `*errs.Fault` values never matches when
a driver is involved — `Detail.Driver` is an `error` (`errs/fault.go:28`) and
`wrapped` is unexported (`:51`) — so a table test degrades to `errs.AsFault` plus
field-by-field comparison, and a path comparison needs its own loop because
`errs.Path` is a slice of `Step` (`errs/path.go:34`). There is no `errs.HasCode`,
no `errs.At(path).Code(c)` matcher, nothing. The library ships `crud/crudtest`
for repositories and nothing for faults. Every consumer writes the same fifteen
lines, and the fourth service's copy is the one with the bug.
**If not ready:** They write it. The honest ask is small and belongs in the
replaceable half: a `errs.FindViolation(f *Fault, path Path, code Code) (Violation, bool)`,
or a tiny `errs/errstest` package, so the fifteen lines are written once here
rather than four times per consumer. Round 1 spent a whole case on a CI check for
translation coverage (H-ERRS-06) and none on the test every consumer writes on
day two, which is the wrong order of importance.

## The DX this should have

### The call site

```go
// a rule of my own, refusing one field
return errs.Validation().Field("Email").Code(errs.CodeInvalidFormat).Fault()

// everything a validation library found, in the same expression
return errs.Validation().
    Violations(errs.FromFieldViolations("CreateUserRequest", verrs...)).
    Wrapping(err).
    Fault()
```

The first line exists today: 1 expression, 3 concepts. The second does not.
Today it is 3 statements and 4 concepts — the bridge, the builder, the fact that
`Fault` returns a value whose slice you may still write to, and the write itself
— and the module page's own version of it does not compile.

Product vocabulary should be difficult to misdeclare: retain `Code` and `Kind`
as their compatible public value types, but make `Codes.Add` reject `Code("")`
and every kind outside the declared constants. A small `Kind.Valid` predicate (or
an unexported equivalent used by `Add`) is sufficient; callers then fail during
wiring instead of constructing a syntactically valid empty code or an arbitrary
numeric kind. `KindInternal` remains the deliberate zero value for an
*undeclared* fault (`errs/code.go:72-90`), not an opt-in vocabulary declaration.

`Violations(vs []Violation) *Builder` takes a **slice** and not a variadic. The
variadic form makes the one line the proposal exists to shorten read
`Violations(errs.FromFieldViolations("CreateUserRequest", verrs...)...)` — two
spreads in one expression — and merging two sources is two calls either way, so
nothing is lost. It appends and leaves `open` at the last appended violation,
exactly as `At` does. An empty slice leaves `open` where it was:
`FromFieldViolations` returns nil for no input (`errs/bridge.go:47-49`), and an
append of nothing must not silently move the cursor, or a following `.Message()`
lands on `Fault.Message` and reaches blocker 15's trap through the new method.
It does not copy: `Builder.Fault` already deep-copies paths, params and column
lists on the way out (`errs/build.go:192-215`), and a second copy site is a second
place to keep honest.

### Turning one knob

Two things exist today and the module page shows neither. Build the renderer
once, per process, and pass it per resource:

```go
codes := errs.StandardCodes()
if err := errs.ValidatorCodes(codes); err != nil {   // does not exist: gte, oneof, email, …
    return err
}
if err := codes.Add("too_young", errs.KindValidation, "must be at least 18"); err != nil {
    return err
}

cat, err := errs.LoadMessages(codes, messages, "messages")
if err != nil {
    return err
}

rd := crudhttp.NewRenderer(crudhttp.WithCodes(codes), crudhttp.WithMessages(cat))

users := crudfiber.New(repo, crudfiber.WithRenderer[User, int64, UserUpdate](rd))
```

That works on all four bindings, today, and it is not in any document. A supplied
renderer is a replacement, not an option bag: `build` uses it directly, and adds
`port.Hops(svc, mapper)` only when it constructs the default renderer
(`crud/http/crudfiber/handler.go:115-124`; the same seam is in `crudnet` and
`crudgin`). A resource with a mapper must therefore put its declared hops into
the supplied renderer itself. Two things are still missing and they are
independent:

```go
// (a) proposed API: the code the client reads, decided by the consumer.
// Neither WithCodeMapper nor CodeMapperFunc exists until CodeMapper is wired.
rd := crudhttp.NewRenderer(
    crudhttp.WithCodes(codes),
    crudhttp.WithMessages(cat),
    crudhttp.WithCodeMapper(errs.CodeMapperFunc(
        func(f *errs.Fault, v errs.Violation) (errs.Code, bool) {
            if v.Code == errs.CodeUnique && pathIs(v.Path, "email") {
                return "email_taken", true
            }
            return "", false
        })),
)

// (b) a replacement renderer keeps the generated/service hops explicitly.
// `svc` is the service passed to ServingFor; for NewFor it is the equivalent
// service the application assembled before choosing this custom renderer.
rd := crudhttp.NewRenderer(
    crudhttp.WithCodes(codes), crudhttp.WithMessages(cat),
    crudhttp.WithResolvers(port.Hops(svc, mapper)...),
)
users := crudfiber.ServingFor(svc, mapper,
    crudfiber.WithRenderer[User, int64, UserUpdate](rd))
```

**(a) is shippable before the tag and (b) is explicit wiring, not a new binding
option.** (a) is the option on `port/porthttp` and `crud/rpc/crudgrpc`, plus the
call inside `port.Violations`; it is usable on day one through `WithRenderer`,
and it is the only answer H-ERRS-16 has. (b) follows the existing order exactly:
`port.Hops` returns the service resolver first and the generated mapper resolver
second (`port/path.go:47-63`); `errs.Chain` applies that list in order
(`errs/spi.go:48-72`); and `crudhttp.WithResolvers` installs it ahead of the
raw-body fallback (`port/porthttp/render.go:61-65`). One local
`type userOpt = crudfiber.Option[User, int64, UserUpdate]` per resource is what
makes that bearable, and it is the pattern the binding's own godoc already
teaches.

The DX change is documentation and a constructor helper, not a panic: replacing
the renderer is already the documented contract of `WithRenderer`
(`crud/http/crudfiber/options.go:72-77`). A helper that accepts render options
and appends `port.Hops` may reduce the repetition, but it must preserve the same
service-then-generated order and must not pretend a replacement renderer carries
configuration it was never given.

The mapper's key is `(Code, Path)` and not the constraint. That is deliberate and
it is the portable one: `CodeFor` receives the whole violation (`errs/spi.go:30`)
and every engine supplies a code. A constraint-keyed mapper is a **PostgreSQL
affordance, and only where `Origin` is `OriginState`** (`errs/violation.go:64`):
on MySQL, MariaDB and SQLite `Source.Constraint` is empty on every arm
(`errs/sqlerr/mysql.go:45-51`, `errs/sqlerr/sqlite.go:46-61`), so such a mapper
never fires and there is nothing to grep for when it does not; and on InnoDB every
table's primary key is literally named `PRIMARY` (`docs/modules/en/sqlerr.md:85`),
so `"PRIMARY": "duplicate_id"` renames the code on every table in the schema. It
reads `v.Source.Constraint` and never `f.Detail.Constraint`, because `Detail` is
fault-level and holds whichever constraint the statement died on: a probe result
carrying three violations from three indexes would otherwise get all three
renamed to one.

Where the call goes is not free either. The required precedence is: synthesise a
violation when the fault has none (`port/violations.go:61-70`); apply the
`CodeMapper` to each resulting violation; reject an empty or undeclared mapped
code; resolve the resulting kind through the wired `Codes`; then sort, cap and
render messages. `errs.SortViolations` is currently at `port/violations.go:90`
and the message ladder at `:94-97`, so mapping after either would produce an
order or a sentence for a code no longer in the body. Finally resolve the response
status from those mapped, declared kinds, not from the original fault: current
HTTP resolves it before `Violations` (`port/porthttp/render.go:127-139`) and
gRPC does the analogous pre-detail resolution (`crud/rpc/crudgrpc/status.go:241`).
That recomputation is required by [[D-049]]: it preserves one kind-to-status
mapping and never creates a code-specific status table.

And on the reading side, for a log:

```go
if f, ok := errs.AsFault(err); ok {
    port.Logger(ctx).Error("write refused", "fault", f)
}
```

Three lines and two concepts, against about eight lines and five field names
today. `func (f Fault) LogValue() slog.Value` is the method, on a **value**
receiver for the same measured reason `Violation.MarshalJSON` has one
(`errs/violation.go:78-82`): a pointer receiver is bypassed when a fault is
logged as a struct field or a map entry, which is exactly the shape a middleware
logs. Not `Dump() string`, because the on-call engineer's aggregator wants
queryable `sqlstate`, `constraint` and `table` attributes rather than a blob to
regex. Not `Detail.String()`, because `fmt` promotes a `Stringer` on a nested
struct field, so a `Detail` that printed the constraint would make `%v` on **any**
struct holding one leak it — the implicit path the module already closed by
putting `MarshalJSON` on value receivers.

`log/slog` is standard library, so this costs nothing under the seal:
`scripts/checks.sh:TIER0_SEALED` puts `errs` in the sealed arm and `check-tiers` runs it with
`-test`, admitting the standard library and `errs/...` and nothing else, and
`errs/doc.go:38-40` requires an empty `require` block at the tag ([[D-036]],
[[D-016]]). Worth saying out loud, because for a package whose whole subject is
the tag, the first question about any new method is what it costs to import.

### Why this shape

Because the module is already one expression per intent everywhere else, and the
missing pieces are the places where the chain stops and the consumer has to fall
out of it: assign a field on a returned struct, rebuild a fault from scratch to
change one word, format five fields to log one failure, hand-write a hop back
that an option silently took away, and compare two faults field by field in a
test. Each is small on its own. Each turns a one-line intent into a paragraph,
which is the shape of ceremony that gets copy-pasted wrong into the fourth
service.

The alternative to `Builder.Violations` is what the docs already show, and it
does not compile. The alternative to wiring `CodeMapper` is deleting it — a worse
answer, because the use case behind it arrives in month two, because H-ERRS-16
needs it in month zero, and because an interface that looks like the answer and
is wired to nothing is worse than its absence: a consumer implements it and
waits.

`errs.ValidatorCodes(c *Codes) error` is the one addition that is not about
ceremony. Every bridged tag is an undeclared code today, which means no default
sentence and invisibility to `Missing`. It returns an `error` because
`Codes.Add` does (`errs/codes.go:120-132`) and a consumer who declared `email` as
`KindConflict` first gets a refusal that needs somewhere to go. It is named for
what it mirrors — `FromFieldViolations` — and its godoc must name the validator
version whose tag list it covers, with the guard test in `test/bridge/`, the one
place allowed to import a validator (`errs/bridge.go:12-16`). Its templates may
use `{param}` and `{value}` and nothing else: those are the only keys the bridge
sets (`errs/bridge.go:62,68`), and per blocker 5 any other placeholder ships raw.

### What it must not break

- **`errs/doc.go:44-56`** — the symbol freeze, which is this package's own
  paragraph and is governed by no decision doc. `Builder.Violations`,
  `ValidatorCodes` and a test helper land in the replaceable half (*"the rest of
  `Builder`"*, *"`Codes` and `StandardCodes`"*). `Fault.LogValue` does **not**:
  `Fault` appears only in the frozen list. The honest ask is to amend that
  paragraph and `docs/api/surface.md`, not to claim coverage. `CodeMapper` is
  already in the frozen half — wiring it is honouring the freeze, not amending it.
- **[[D-036]], [[D-016]]** — `errs` keeps an empty `require` block and
  `TIER0_SEALED` admits the standard library only. Everything proposed here is
  stdlib (`log/slog`) or no import at all.
- **[[D-048]]** — cited to say what it does *not* govern. Its invariant is which
  **packages** join the contract manifest; it says nothing about which symbols
  the first tag freezes inside `errs`. It does bear on H-ERRS-11: `errs/sqlerr`
  is on that list, so the corpus tooling it exports is frozen with it.
- **[[D-047]]** — `Error()` and `String()` remain classification only: no cause,
  driver text, DSN, token, customer value, `Detail`, `Source`, `Message` or
  `Params`. `Fault.Error` implements that boundary (`errs/fault.go:57-104`) and
  `Fault.MarshalJSON` keeps the same material out of the public projection
  (`:106-148`). A future structured log helper may expose vetted diagnostic
  fields, but must not turn `Error()` into a cause channel; callers that walk
  `errors.As` own redaction of the underlying error.
- **[[D-044]]** — nothing internal on the wire. A `CodeMapper` keyed on
  `Source.Constraint` reads an internal name **inside** the process and emits a
  word the consumer chose. Same trade the message ladder already makes with
  `Params`. H-ERRS-16 is a gap in D-044's coverage rather than a challenge to it:
  D-044 weighed the echoed value and did not weigh the code-and-path pair, and
  the sweep's position is that it should, with `CodeMapper` as the answer.
- **[[D-046]]** — H-ERRS-09 does **not** challenge it. D-046 chose the `restrict`
  collapse deliberately and wrote down why, and the round-1 draft called that
  choice an accident. The retryable-code divergence is a different family, in the
  same tables, that D-046's **What it forbids** does not reach.
- **[[D-049]]** — the kind decides the status. An overridden code must be a
  declared code, resolve through the wired vocabulary, and have its status
  recomputed afterwards; otherwise the mapper would create a second status table.
- **[[D-043]]** — a `CodeMapper` changes a code and never a path. The canonical
  one-hop resolver sequence remains service then generated mapper, followed only
  by the raw-body fallback when neither declared hop changed the path. A custom
  renderer must receive that same sequence explicitly.
- **[[D-019]]** — H-ERRS-09 challenges it, narrowly. The decision says a dialect
  difference is either compensated for or on a list of eleven, and the retryable
  code split and PostgreSQL's `25P02` status are on neither. Closing it is a
  decision, and the sweep's position is that the list should grow rather than the
  classifier.
- **[[D-056]]** — nothing here challenges it. It does leave the wrong half fixed:
  the trap is recorded in a document and the godoc that states the opposite is
  still shipping.
- **[[D-015]]** — the 500 says nothing, and H-ERRS-15 does not ask it to. A
  correlation id is not a message and not a driver's sentence; the ask is a
  handle, and where it goes is a question for `port/porthttp`.

## DX verdict

Distance is how much a consumer types. Reversible says whether the tag closes the
door — an irreversible row changes bytes a shipped client already parses, or
freezes a signature, and cannot ship in a point release.

| What the ideal asks for | Today | Engines | Distance | After the tag |
|---|---|---|---|---|
| A hand-written refusal in one expression | Exactly that — 1 expression, 3 concepts | all | none | — |
| Pre-built violations into a fault, mergeable | 3 statements, 4 concepts, and a write through a value `Builder.Fault` deep-copies on purpose; or a 7-call loop per violation; the module page's own example does not compile | all | small | reversible |
| A bridged validator tag that renders as a sentence | Declare every tag by hand in `Codes`, or ship `{"error_code":"gte","message":"gte"}` — status is correct either way | all | small | reversible |
| Rename a code for the client | Nothing. Rebuild the fault by hand and lose the wrapped sentinels doing it — the frozen interface for it is called from no line in the repository | all (keyed on code/path); constraint-keyed: pg only | large | **irreversible** — the signature freezes |
| Refuse to confirm an address exists, on one route | Nothing. Same rebuild, or accept the disclosure | all | large | reversible once the mapper is wired |
| Declare product codes and get statuses on a generated route | `Add` per code, then one `WithRenderer` with a shared renderer — undocumented; a replacement renderer on `NewFor`/`ServingFor` must receive `WithResolvers(port.Hops(svc, mapper)...)` explicitly | all | small | reversible |
| Locale files and a narrow override on a generated route | `LoadMessages` plus the same explicit renderer composition when the resource has path hops | all | small | reversible |
| A vocabulary default with a placeholder in it | Do not write one. Below `errs.Messages` the template goes to the client raw, with or without a catalogue | all | small | reversible |
| Plurals in a second language | Replace `errs.Messages` wholesale. The SPI is there and nothing says the flat catalogue stops at one language | all | large | reversible |
| Fail CI on a half-translated locale | `cat.Missing("ru")` in a test — which reports nothing while a `default.json` covers the code | all | large | reversible |
| One call for a structured diagnostic log line | ~8 lines, 5 field names, and `slog.Any("detail", f.Detail)` spills | all | small | reversible |
| A 500 you can trace to a customer's ticket | Nothing in the body, nothing in a log. The consumer replaces `Internal()`, which means their own renderer | all | large | reversible |
| A retryable/not-retryable branch | `errs.AsFault` then compare the kind, or `port.KindOf` | all | none | — |
| The same `error_code` on every engine | Not a thing you write — a thing you discover. `serialization_failure` is three words on four engines and SQLite refuses three classes of bad value not at all | — | large | **irreversible** |
| The same status for one `InTx` on every engine | Nothing. PostgreSQL answers 503 for the second statement and the client retries a poisoned transaction | pg differs | large | **irreversible** |
| A contract for a code a client has never seen | Nothing written. The kind is not on the wire, so the fallback is the status and nobody said so | all | large | **irreversible** — `exclusion` cannot ship without it |
| Classify a driver error with no repository | `sqlerr.Classify(dialect, e)` — one pure call; the flattening is `crud/sqlfault`'s and brings `crud` plus `crud/catalog` | all | none | — |
| Write a classifier for a non-SQL store | Implement one method — and every `errors.Is` above goes quiet unless you wrap the right sentinel by hand, with no list of which | all | small, once you know | reversible |
| `errors.Is` on a sentinel after wiring faults | Unchanged, by construction | all | none | — |
| Read a failure body back | `porthttp.ParseEnvelope` — correct, untested anywhere, and unreachable from the type a consumer starts at | all | small | reversible |
| Assert a fault in your own test | ~15 hand-written lines per consumer; `reflect.DeepEqual` never matches | all | large | reversible |

**Overall:** the core reads like the ideal and in several places beats it — the
value receivers on every marshaller, the corpus behind the dialect tables, the
sentinel living inside the fault, `KindOfWith` refusing to read an unknown code
as internal, and `Missing`'s refusal to be a start-up check are all better than
what I would have designed blind. Round 1 said the vocabulary and the catalogue
were unreachable from a generated route and that was wrong: they are one
undocumented option away. What is actually wrong is quieter and worse. The short
path and the customised path both work, but a replacement renderer needs an
explicit declaration of its path hops; `Missing` walks past the default rung, a
catalogue silently overrides a violation's own text, and a second `Add` silently
keeps the first sentence. None of those is wordiness. Each is a contract choice
the library must state plainly before the tag freezes it.

## Release blockers found here

Owner is where the change lands. Four of these twenty-one are fixed inside `errs`
alone (6, 8, 9, 17); the rest span `errs/sqlerr`, `port`, `port/porthttp`, the
bindings and the docs, and the `port` sweep reports several of the same rows.

| # | What | Severity | Owner | Why it blocks |
|---|---|---|---|---|
| 1 | The retryable `error_code` is three different words on four engines — `serialization_failure` / `deadlock` / `lock_timeout` for one provoked failure — and [[D-019]], which says *"There are eleven"* observable differences, lists no code-level difference at all | blocker · **irreversible** | `errs/sqlerr` + [[D-019]] | The one field the whole contract exists to give a client is engine-dependent, and the test whose name promises the check (`TestEveryEngineAnswersTheSameQuestions`) compares only case names, never `Want`. Fixing it after the tag changes what a shipped client parses. Distinct from the `restrict` split, which [[D-046]] chose on purpose |
| 2 | No written contract for a code a client does not recognise, and the kind is not on the wire | blocker · **irreversible** | `errs` docs + `docs/modules/en/errs.md` | Every additive change downstream — `exclusion`, a consumer's `email_taken`, a new `sqlerr` arm — is a breaking change until somebody writes "branch on the code, default to the status". One paragraph before the tag makes rows 1 and 3 survivable instead of fatal |
| 3 | `errs.CodeExclusion` is declared, kind-mapped, given a default sentence and produced by no engine; `23P01` has no arm and `sqlfault`'s class-23 gate turns it into a bare 409 `conflict` | blocker · **irreversible** | `errs/sqlerr` + roadmap | Closing it is wire-visible: a client handling `conflict` starts receiving `exclusion`. `docs/roadmaps/Roadmap.md:102-115` records it as a deliberate hole, which is fine — what is not fine is shipping the code in the vocabulary with no contract (row 2) saying a client must tolerate it |
| 4 | `errs.CodeMapper` is frozen into the contract (`errs/doc.go:51`) and no line in any module calls `CodeFor` | blocker · **irreversible** | `errs` (freeze) + `port/porthttp` + `crud/rpc/crudgrpc` + `port.Violations` | The tag freezes an SPI signature wired to nothing, and it is the only answer H-ERRS-16 has. Half (a) — the option plus the call in `port.Violations` — is usable on day one via `WithRenderer` and ships before the tag; if it cannot, `errs/doc.go` and `errs.md:437` must say the interface is declared and not consumed |
| 5 | `errs/sqlerr` exports `Path`, `Save`, `Load`, `Corpus` and `Case` (`docs/api/surface.md:538-547`) and is in the contract manifest | blocker · **irreversible** | `errs/sqlerr` | The tag freezes the testdata JSON shape as a compatibility surface. Either that is deliberate and said so, or the capture tooling moves to an internal package. Free before the tag, permanent after |
| 6 | `errs.Messages` and `errs.Codes` have no synchronisation, and `docs/modules/en/errs.md:421` says `Messages.Load` *"adds a locale at run time"* | blocker | `errs` | A documented invitation to a data race in the one package every request goroutine reads through `port.Violations`. No test can see it because no test loads while rendering. Either the sentence says start-up only, or `Messages` gets an `RWMutex` |
| 7 | A custom renderer on `NewFor`/`ServingFor` needs the existing `port.Hops(svc, mapper)` composition documented | serious | `crud/http/{crudnet,crudfiber,crudgin}` + `crud/rpc/crudgrpc` docs | `WithRenderer` replaces the renderer by contract; its resolver list cannot be inferred from the interface. The documented composition is service hop, then generated mapper hop, ahead of fallback. Also the `port` sweep, *The client hears the message in its own language* |
| 8 | Below `errs.Messages`, a vocabulary default is handed to the body unexpanded — `port/violations.go:126` → `Codes.MessageFor` — so a declared `"at most {max} characters"` ships `{max}` | serious | `port` (3 lines in `defaultMessage`) + `errs` docs | It is the exact template `docs/modules/en/errs.md:394` teaches, and it fires **with** a catalogue wired for any key that catalogue does not cover — so wiring one is not the mitigation round 1 implied. `Messages.Message` returns `("", false)` and the rung below has no guard. It also falsifies [[UC-017]] guarantee 4 — *"falling back rather than emitting a template"* — whose status in `docs/ai/usecases/Index.md:88` is **covered**: either that row moves to partially covered or the guarantee is narrowed to `errs.Messages`, and only this sweep is placed to say which |
| 9 | Bridged validator tags (`gte`, `email`, `oneof`) are undeclared codes, so the rendered message is the tag: `{"error_code":"gte","message":"gte"}` | serious | `errs` | The bridge's promise is a code *and* a sentence. Nothing in the docs says the tags must be declared, and `Missing` cannot see them because they are not in the vocabulary. `errs.ValidatorCodes` closes both. The **status is correct** — `port.KindOfWith` refuses to read an unknown code as internal (`port/kind.go:28-46`) — and round 1 said otherwise, which oversized it |
| 10 | On PostgreSQL alone, every statement after a failure in the same transaction is `25P02` → `transaction_aborted` → `KindRetryable` → **503** | serious · **irreversible** | `errs/sqlerr` + [[D-019]] + `docs/modules/en/sqlerr.md` | A well-behaved client retries a permanently poisoned transaction, forever. It is the one dialect difference that moves the **status** rather than the word, it is on no list, and a consumer who develops on SQLite never sees it |
| 11 | `docs/modules/en/errs.md:71-99` shows a two-line set-up and a 409 carrying `"field":["email"]`; that wiring produces no field on any engine | serious | docs | The module's first-five-minutes example promises the thing the module is for and cannot deliver it. `TestOnlyPostgreSQLFillsInASource` proves the premise; the usage guides get it right and the front page does not |
| 12 | Nothing in the library logs a fault, and a 500 body carries `error_code` and nothing else — no id, no handle | serious | `port/porthttp` + `errs` (`LogValue`) + docs | The most common failure a consumer ships is invisible on both ends. `port.Logger` exists and no renderer calls it; `errs.Fault` has no field for a correlation id. Either the library logs it or the documentation says plainly that it is the consumer's, and neither is true today |
| 13 | `unique` at `email` on an unauthenticated form is account enumeration, and the only seam for changing it is row 4 | serious | `errs` (via row 4) + [[D-044]] + docs | [[D-044]] weighed the echoed value and never the code-and-path pair, which is sufficient on its own. The flagship example teaches the disclosure. It raises row 4 from an unused interface to a compliance requirement with no answer |
| 14 | `Messages.Missing(locale)` walks to the default rung, so a code with English text in `default.json` is reported as covered for `ru` | serious | `errs` | The one CI check the module offers is green on exactly the case it was added for, and no `Missing` fixture pairs a `default.json` with a second locale. What a consumer needs — a locale's declared keys — is not exposed |
| 15 | No builder step takes `[]Violation`, so `FromFieldViolations` has no one-expression way into a fault; `docs/modules/en/errs.md:452` shows a snippet that does not compile | serious | `errs` | The documented headline of the validation bridge cannot be written as documented. The three-line workaround writes through a value `Builder.Fault` deep-copies on purpose, and `port.FaultFrom` — which looks like the missing step and is exported — silently drops `Params` and `Source` |
| 16 | Nothing exists for testing a consumer's own faults: `reflect.DeepEqual` never matches, and every consumer writes the same ~15 lines | serious | `errs` (or a new `errs/errstest`) | `Detail.Driver` is an `error` and `wrapped` is unexported, so a table test degrades to hand comparison and a loop over `errs.Path`. It is the test every consumer writes on day two and the library ships nothing for it |
| 17 | `Violation.Approximate` never reaches the wire, and no document says what a `field` is worth | serious | `errs` godoc | A form receiving `"field":["Email"]` from a declined hop is byte-indistinguishable from a correct one, so the UI marks the wrong input or nothing. Dropping it may well be right ([[D-044]]); the defect is that nobody weighed it or wrote the guarantee down |
| 18 | A violation's own `Message` is discarded whenever a `MessageSource` is wired, because `errs.Messages` answers from the vocabulary default for any declared code first | sharp edge | `errs` | Same fault renders two different sentences depending on one unrelated start-up call. There is no longer path to fall back to, so the honest advice is never to use `Builder.Message` on a violation — and the godoc says the opposite |
| 19 | `Fault.Message`'s godoc (`errs/fault.go:45`) says *"never rendered"*; `port/violations.go:69,123` renders it on a violation-free fault. And a second `Codes.Add` of the same code with the same kind silently keeps the first sentence | sharp edge | `errs` godoc + `port` | Both are documented deliberate choices whose consequence is invisible. Every fault this library builds already carries a violation, so [[UC-015]] guarantee 11 holds — but a consumer who reads `go doc` and builds their own ships a diagnostic to clients, and two libraries declaring `too_long` never learn they disagreed |
| 20 | `errs/fault.go:63-67` states in the present tense that `porthttp` copies `err.Error()` into every sub-500 body and calls `MarshalJSON` the debug channel; [[D-047]]:57-58 repeats the second claim | sharp edge | `errs` + [[D-047]] | Both are false: the envelope replaced that body and `MarshalJSON` drops `Detail`. It is the frozen contract's own file telling the next reader a leak exists that does not. D-047 needs only that one sentence corrected — its `:60-62` already anticipated the envelope |
| 21 | `errs/doc.go:47` freezes *"[Kind] and its eight constants"*; `errs/code.go:77-90` declares nine, and `docs/modules/en/errs.md:223` says *"Nine values"*. Beside it, shipping godoc in the frozen half carries phase-numbered notes (`errs/code.go:63-70`, `errs/violation.go:146`, `errs/codes.go:56`) citing `ROADMAP-errors.md §N` | sharp edge | `errs` | At the tag that paragraph is the document saying what may never change, and a reader counting eight will wonder which one is not covered. A consumer reads the notes with `go doc errs.Kind`: internal build history in the half that gets frozen, and cheaper to fix than any other row here |

## Contested

- **The renderer replacement boundary is retained, not called a dropped hop.**
  `crudfiber.New(repo, WithRenderer(NewRenderer(WithCodes, WithMessages)))` needs
  no hop because `New` uses `port.Identity`; `NewFor` and `ServingFor` do. The
  binding uses a supplied renderer directly and constructs `rendererFor(Hops)`
  only when none was supplied (`crud/http/crudfiber/handler.go:115-124`). Since
  `crudhttp.Renderer` is only `Render(ctx, err)`, it has no resolver list a
  binding can merge. The truthful DX is the explicit `WithResolvers(port.Hops(svc,
  mapper)...)` composition, not a non-existent `WithRenderOptions` or a panic.
- **`Builder.Violations` leaves `open` at the last appended violation.** A
  reviewer asked for `-1`. With `-1`, a following `.Message(...)` lands on
  `Fault.Message` — row 19's trap, reached through the new method — and a
  following `.Params(...)` opens a phantom empty violation after twenty real
  ones. The empty-slice case is the exception and is spelled out in the DX
  section.
- **`Builder.Violations` does not copy.** A reviewer asked it to clone paths,
  params and column lists on append. `Builder.Fault` already deep-copies all
  three on the way out (`errs/build.go:192-215`), so the produced fault is safe;
  a second copy site is a second place for the rule to rot.
- **`Fault.Dump() string` dropped rather than kept beside `LogValue`.** A
  reviewer wanted it retained. Two renderings of the same internal half is two
  things to keep honest against [[D-047]], and the string one is the one that
  ends up in a ticket screenshot.
- **H-ERRS-12 kept as a case rather than folded into a "what already holds"
  list.** A reviewer called it dilution because every must-hold is green. It
  stays because its must-hold 3 is exactly what H-ERRS-11 must-hold 3 fails at: a
  reader who sees only the tick concludes sentinel matching is a property of
  faults, when it is a property of `Builder.Wrapping`, and that is the mistake a
  third-party classifier author makes.
- **H-ERRS-11 kept here rather than handed entirely to the `crud/sqlfault`
  sweep**, and re-aimed. Reviewers across two rounds called it sqlfault's, and
  the wiring is. The case now asks what only this module can fail at — writing a
  `Classifier` against the frozen SPI, and `sqlerr.Classify` being pure and total
  — and carries row 5, which has no other home.

## Edge cases

### E-ERRS-01 — A zero-value code reaches the form
**Shape:** degenerate declaration
**Setup:** A service reads its product codes from configuration and registers a zero-value `errs.Code` by mistake.
**What the consumer does:** It returns a field violation carrying that code and expects bad start-up configuration to fail before any route serves traffic.
**What must happen:** A code that is empty must be refused at declaration time; an omitted machine word must not become a distinct, empty `error_code` that a client has to interpret.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/codes.go:120-131` accepts every `Code` value; `errs/violation.go:83-110` always emits the value as `error_code`; and `errs/message.go:116-149` has no lookup keys for an empty code. No test found for declaring or rendering `Code("")`.
**Blast radius:** silent wrong answer

### E-ERRS-02 — A typoed kind becomes an internal response
**Shape:** degenerate declaration
**Setup:** A product vocabulary passes a value such as `errs.Kind(99)` to `Codes.Add` after decoding an application setting.
**What the consumer does:** It expects the declaration to fail at start-up, because the code is about to become part of a response contract.
**What must happen:** Only the nine declared kinds may be accepted; an unrecognised kind must not be retained as known and then spell itself `internal` on the wire.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/codes.go:120-140` stores and reports an arbitrary `Kind`; `errs/code.go:75-115` makes every unrecognised value render as `internal`. `TestTheZeroKindIsInternalAndSoIsAnUnknownOne` (`errs/codes_test.go:124`) pins the latter fallback, but no test passes an invalid kind to `Codes.Add`.
**Blast radius:** silent wrong answer

### E-ERRS-03 — A downstream body names row minus one
**Shape:** adversarial input
**Setup:** A gateway decodes a downstream path containing `[-1]`, or a service builds `errs.Indexed(-1)` by hand.
**What the consumer does:** It forwards or logs the refusal and expects a field position to address a real element.
**What must happen:** Negative positions must be refused before they can become a public path; there is no minus-first item for a client to mark.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/path.go:24-25` constructs any integer, and `errs/path.go:76-106` unmarshals any JSON integer without a sign check before calling it an index. `TestABracketedNegativeNumberStaysPartOfTheName` (`errs/path_test.go:142`) protects only the dotted parser, not the constructor or JSON decoder.
**Blast radius:** silent wrong answer

### E-ERRS-04 — A missing field name looks like a real field
**Shape:** misuse
**Setup:** A handwritten validator reads a missing configuration value and calls `Field("")` before attaching a code.
**What the consumer does:** It expects an invalid field declaration to fail loudly, or at least become a general violation rather than a field a form cannot address.
**What must happen:** A non-general path must contain meaningful member names; `field:[""]` must not be a plausible-looking response.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/build.go:64-71` turns the empty string into an opened path with no validation, and `errs/path.go:43-61` serialises that one empty step. No test found for `Field("")`.
**Blast radius:** silent wrong answer

### E-ERRS-05 — A JSON member contains a dot
**Shape:** boundary
**Setup:** A request really has a member named `billing.email`, and the service records its `errs.Path` in a log before another component parses that text.
**What the consumer does:** It needs the wire path to remain exact and the dotted rendering not to be mistaken for a machine round-trip format.
**What must happen:** `errs.Path`'s dotted/bracketed form remains the canonical
human-facing Errs grammar, while the JSON array form preserves a literal member
exactly. A consumer must not treat `Path.String()` as a recovery format when a
member itself contains the grammar's separator.
**Today:** 🟡 partial
**Evidence:** `errs/path.go:64-106` preserves every JSON string step, while
`errs/path.go:116-119` deliberately makes `String` lossy. The dotted/bracketed
grammar is what `ParsePath` accepts (`errs/path.go:122-166`); its test includes
the control showing that a separator does not survive a text round-trip
(`TestParsePathRoundTripsTheDottedForm`, `errs/path_test.go:77-123`).
`docs/modules/en/errs.md:242-253` introduces the renderings without the recovery
warning.
**Blast radius:** confusing error

### E-ERRS-06 — A returned fault retains a caller-owned parameter value
**Shape:** concurrency
**Setup:** A batch validator puts a mutable slice or map inside `Params`, returns a fault, then reuses and changes that value for the next row.
**What the consumer does:** It treats `Fault()` as a snapshot, as its godoc promises, and renders the first fault later on another goroutine.
**What must happen:** Either all parameter values are immutable snapshots or the contract must say that `Params` values themselves stay caller-owned and must not be shared.
**Today:** 🟡 partial
**Evidence:** `errs/build.go:184-217` promises a copy "all the way down" but copies only the outer `map[string]any`; `errs/build.go:205-209` assigns each `any` value unchanged. `TestAFaultDoesNotShareASliceWithTheBuilderOrTheCaller` (`errs/build_test.go:285`) covers paths and column slices, not a slice or map held inside `Params`.
**Blast radius:** silent wrong answer

### E-ERRS-07 — Two requests reuse one builder
**Shape:** concurrency
**Setup:** A service caches a partly configured `*errs.Builder` and two handlers add their own field and message before calling `Fault`.
**What the consumer does:** It reuses the apparent template to reduce repeated setup and has no reason in the public builder docs to know that this is unsafe.
**What must happen:** The builder must either be safe to share or state plainly that it is a one-fault mutable value which must not cross requests.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/build.go:68-70`, `:88-94`, `:100-108` and `:175-181` mutate the same `Builder.f` and `open` without synchronisation. No concurrent-builder test or public concurrency warning was found.
**Blast radius:** silent wrong answer

### E-ERRS-08 — A failed catalogue reload installs half the change
**Shape:** partial failure
**Setup:** An operator calls `Messages.Load` with a directory whose first file is valid and whose later file has a non-string value.
**What the consumer does:** It receives an error and expects the catalogue it was already serving either to be unchanged or to have an explicit transactional reload contract.
**What must happen:** One `Load` must be all-or-nothing, or its partial mutation must be documented so a caller can discard the receiver rather than serve a mixture of old and new wording.
**Today:** ❓ unverified
**Evidence:** `errs/catalogue.go:86-93` adds files directly to the receiver as it
walks them, and `errs/catalogue.go:109-129` adds keys before a later key can
fail. That implementation makes partial mutation plausible, but
`TestTwoFilesDisagreeingOnOneKeyAreRefused` (`errs/catalogue_test.go:103`) checks
only the returned error, not the receiver after a failed load; no focused reload
atomicity control was found.
**Blast radius:** silent wrong answer

### E-ERRS-09 — `pt-br` misses a `pt-BR` catalogue
**Shape:** boundary
**Setup:** The catalogue is named `pt-BR.json`, while a transport passes the lower-case language tag it received from a client.
**What the consumer does:** It expects the same language to select the same wording independent of the tag's casing.
**What must happen:** Locale matching must canonicalise case, or the file-name convention must say it is case-sensitive before an API silently falls to its default language.
**Today:** ❓ unverified
**Evidence:** `errs/message.go:159-170` preserves the supplied locale and only
splits it on `-` or `_`; `errs/catalogue.go:98-105` preserves the file-name
spelling too. `TestAPOSIXLocaleFallsBackTheSameWayAHyphenatedOneDoes`
(`errs/message_test.go:233`) covers separator choice, not case; there is no
focused case-normalisation control.
**Blast radius:** confusing error

### E-ERRS-10 — A translator leaves an unmatched brace
**Shape:** adversarial input
**Setup:** A release contains `"at most {max characters"` or `"{}"` in a locale file.
**What the consumer does:** It expects a catalogue typo to stop start-up, or at least to fall through rather than show template syntax to a client.
**What must happen:** Malformed placeholders must be rejected when the catalogue is loaded or be treated as unresolved; literal brace syntax must not look like a successful message.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/message.go:178-209` copies an unmatched `{` and an empty `{}` into a successful result; `errs/catalogue.go:109-129` validates only JSON shape and string values. `TestATemplateWithAMissingParamFallsBackRatherThanEmittingThePlaceholder` (`errs/message_test.go:67`) covers a valid placeholder with a missing value, not malformed syntax.
**Blast radius:** confusing error

### E-ERRS-11 — A flat key can still be impossible to reach
**Shape:** misuse
**Setup:** A translator writes the flat key `order.items.email.unique`, believing the whole request path is supported.
**What the consumer does:** It expects the start-up validation that catches a nested object to catch a flat override the lookup ladder can never use.
**What must happen:** An impossible catalogue key must be refused or reported; a valid JSON file must not silently discard a product's more specific wording.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/message.go:111-149` consults only the first and last named steps, while `errs/message.go:56-74` accepts any key. `errs/catalogue.go:52-63` documents this exact dead key; `TestANestedCatalogueFileIsRefused` (`errs/catalogue_test.go:136`) tests nested JSON but accepts the flat spelling at `:148-152`.
**Blast radius:** confusing error

### E-ERRS-12 — A driver extractor joins the wrong MySQL tuple
**Shape:** adversarial input
**Setup:** An adapter accidentally combines the `23000` SQLSTATE from one driver error with native code `1205` from another.
**What the consumer does:** It passes the flattened error to `sqlerr.Classify` and needs uncertainty to stay unclassified rather than becoming a retryable or conflict response.
**What must happen:** The complete `(dialect, sqlstate, native)` tuple must be required; a tuple not captured for that dialect must produce `ok == false`.
**Today:** 🟡 partial
**Evidence:** `errs/sqlerr/mysql.go:22-50` looks up the exact pair, so `{"23000", 1205}` has no arm. `TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser` (`errs/sqlerr/dialect_test.go:27`) checks several foreign-dialect refusals, but no test found for a mismatched state and native number within one MySQL tuple.
**Blast radius:** none

### E-ERRS-13 — A corpus says two things are named `unique`
**Shape:** degenerate declaration
**Setup:** A recapture or review tool loads a corpus with duplicate case names, a case with no error, or a non-empty `want` that no captured error can support.
**What the consumer does:** It asks `Corpus.Case("unique")` and expects the capture tooling to reject an internally inconsistent release input rather than choose a record by order.
**What must happen:** `Load` must validate case names and the `Want`/`Err` relationship before handing the corpus to the classifier tests.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/sqlerr/corpus.go:154-167` validates only the top-level engine string; `errs/sqlerr/corpus.go:141-149` returns the first duplicate name. No corpus-load validation test beyond malformed JSON and engine mismatch was found.
**Blast radius:** silent wrong answer

### E-ERRS-14 — A capture writes outside its selected corpus directory
**Shape:** adversarial input
**Setup:** Capture automation constructs a `Corpus` from configuration with `Engine: "../baseline"` and calls `sqlerr.Save`.
**What the consumer does:** It expects a helper passed a corpus directory to write only under that directory, even when a capture is malformed.
**What must happen:** The engine must be validated as one supported file stem, or the resulting path must be proven to remain under `dir` before a write.
**Today:** ❌ wrong or unhandled
**Evidence:** `errs/sqlerr/corpus.go:151-152` joins `engine + ".json"` directly into the path, and `errs/sqlerr/corpus.go:174-179` writes that path without validation. `TestSavingAnUnchangedCorpusRewritesNothing` (`errs/sqlerr/corpus_test.go:139`) exercises only the four checked-in engine values.
**Blast radius:** data loss

### E-ERRS-15 — A wrapped database or HTTP error contains a secret
**Shape:** disclosure boundary
**Setup:** A driver or upstream HTTP error includes a DSN, bearer token, or a
customer value in its text. The service builds a classified fault with
`Builder.Wrapping(err)` and returns it through an HTTP or gRPC boundary.
**What the consumer does:** It expects the response and ordinary `Error()` text
to remain safe to display, while still being able to inspect the underlying error
inside trusted, redacting diagnostics.
**What must happen:** `Fault.Error()` and `Fault.String()` must contain no cause;
the public JSON projection and standard internal HTTP response must omit it too.
The cause remains intentionally reachable through `errors.As` / `Unwrap`, so the
application — not the framework — owns redaction before logs, traces or support
tools. gRPC's status renderer is the corresponding transport seam, not a second
Errs reconstruction contract (`crud/rpc/crudgrpc/status.go:241-264`).
**Today:** ✅ handled at the Errs and standard HTTP boundary
**Evidence:** `Builder.Wrapping` puts causes in the fault's unexported wrapped
list (`errs/build.go:175-181`); `Fault.Error` explicitly excludes wrapped error
text and `Unwrap` exposes the list only to Go error traversal
(`errs/fault.go:57-104`); `Fault.String` shares that safe text (`:151-155`); and
`Fault.MarshalJSON` excludes wrapped errors and `Detail` (`:106-148`). The
standard HTTP renderer short-circuits an internal status to `Internal()` before
copying fault content (`port/porthttp/render.go:123-139`).
**Blast radius:** data disclosure if an application logs the cause without
redaction

## Edge verdict

The worst new failure is a silent wrong response assembled from values the
framework could have rejected at start-up: a blank code, an unknown kind, an
empty field and a negative index all have valid JSON spellings but no valid
consumer action. Snapshotting is only shallow for `Params`, and a shared builder
has no concurrency boundary, so an error can change between producer and
renderer. The catalogue refuses several structural mistakes and admits broken
placeholder syntax and unreachable flat keys; failed-reload atomicity and
locale-case matching are still unverified, rather than established release
claims. Wrapped causes are safely absent from Fault text, JSON and the standard
internal HTTP envelope, but their deliberately reachable `Unwrap` path makes
caller-side redaction non-optional. `sqlerr` itself stays conservative on a key
it does not recognise, but the exported corpus utilities do not validate their
own records or write boundary.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `Codes.Add` accepts an empty `Code` and every out-of-range `Kind`, so a typed configuration mistake can reach a client as an empty code or `internal` | serious | The response remains syntactically valid while no client branch can mean what the service author declared. A start-up error is the only honest outcome. |
| 2 | `Path` admits an empty field and a negative index through public construction and JSON decode | serious | A form can be told to mark a field or array element that cannot exist, with no signal that the error body is invalid. |
| 3 | `Fault()` copies only the outer `Params` map and a reusable `Builder` has no concurrency contract | serious | A response can acquire another request's mutable parameter value or race while it is assembled; the result is a plausible but wrong sentence. |
| 4 | `Corpus.Save` lets `Corpus.Engine` escape the requested directory | serious | Capture automation supplied a malformed engine can overwrite a neighbouring JSON file instead of failing in its scratch directory. |
| 5 | Failed `Messages.Load` atomicity and locale-case matching have no focused controls | sharp edge | The implementation invites concern, but a release verdict must not turn unmeasured reload or locale behaviour into a certainty. Add isolated receiver-state and case-normalisation controls before choosing a contract. |
| 6 | Malformed templates and unreachable flat keys load successfully and then fall through or render syntax | sharp edge | Product wording silently differs from the reviewed catalogue, which makes localisation failures hard to diagnose. |
