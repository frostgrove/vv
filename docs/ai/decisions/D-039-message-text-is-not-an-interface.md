# D-039 — Message text is not an interface

**Status:** accepted
**Invariant:** No classification and no field path may be derived from a driver's message text. Columns come from the catalog; a value parsed out of a message is best-effort enrichment whose absence is never an error.

## The decision

A driver error has two halves. The **key** — dialect, SQLSTATE, native number,
and which structured fields the driver populated — is what the classifier reads.
The **text** is recorded, shown to nobody, and never parsed for anything a
caller depends on.

Where a column name is needed and the driver did not supply one, it comes from
the catalog ([[D-041]]), not from the sentence the server wrote.

## Why

**Because three of the four engines localise it.** PostgreSQL renders `Message`,
`Detail` and `Hint` through `lc_messages`; MySQL and MariaDB do the same. A
parser that reads text works on the developer's laptop and stops working on a
server someone deployed with a different locale — and it fails by *misreading*
rather than by erroring, so it produces a confident wrong field path.

**Because the same server rewords itself between patch releases**, and because
the two engines that share a driver do not agree on wording at all. Both are
measured, from the corpus:

| | MySQL 8.4.11 | MariaDB 11.4.12 |
|---|---|---|
| duplicate key | `Duplicate entry 'anchor' for key 'cp_parent.slug'` | `Duplicate entry 'anchor' for key 'slug'` |
| the same, `lc_messages = 'ru_RU'` | `Дублирующаяся запись 'anchor' по ключу 'cp_parent.slug'` | `Дублирующаяся запись 'anchor' по ключу 'slug'` |
| CHECK | `Check constraint 'cp_pos' is violated.` | ``CONSTRAINT `cp_pos` failed for `vv`.`cp_parent` `` |
| bad type | `… for column 'num' at row 1` | ``… for column `vv`.`cp_parent`.`num` at row 1`` |

The index name is table-prefixed on MySQL and bare on MariaDB. A regex written
against one silently extracts the wrong identifier from the other. The Russian
row is the corpus's `unique_in_another_locale` case, captured from the same
servers in the same run: not one word of it survives, and the key does not move.

**Which engines localise, measured rather than assumed.** `postgres:17-alpine`
is built without NLS, so it *accepts* `SET lc_messages` and answers in English
anyway. SQLite ships one set of English strings and has no setting at all. MySQL
and MariaDB localise the duplicate-key and NOT NULL sentences and leave the CHECK
and foreign-key ones in English. So the localised twin is capturable on two
engines out of four, and only for one violation — which is why the invariant
rests on a total substitution test and the twin is what says that test is
substituting something real.

**Because even the value is untrustworthy.** MySQL joins a composite key's
values with `-`, so `('x-1','y')` is reported as `'x-1-y'` and cannot be split
back. On a prefix index the reported value is the truncated prefix, not what was
sent.

**Because the structured half is often empty anyway.** `mysql.MySQLError` has
`Number`, `SQLState` and `Message` and nothing else — the corpus records no
structured fields at all for MySQL, MariaDB or SQLite. Only PostgreSQL fills in
`ConstraintName`, `TableName`, `SchemaName` and `Detail`. So on three engines
out of four every structural fact beyond the number has to come from the catalog,
and pretending it can come from the message is what makes that gap invisible.

**Why record the text at all.** Because a human reading a corpus diff needs to
see what moved, and because a log line with the server's own sentence is worth
having. Recording it and asserting on it are different things.

## What it forbids

- Do not regex a driver message for a constraint, table, column or value.
- Do not assert on message text in a test that is meant to pin classification.
  `TestTheCorpusStillDescribesTheseServers` compares the key and not the text,
  deliberately, and widening it to the text would make the suite fail over a
  reworded sentence and train everyone to stop reading it.
- Do not put a driver message into a response body — that is [[D-044]], and a
  message can contain a SQL string, a column list or a connection fragment.
- Do not treat a missing `Detail` as an error. On PostgreSQL it carries the
  offending value; on the other three there is no `Detail` at all.

## Where it lives

- `errs/sqlerr/corpus.go:Err` — `SQLState`, `Native` and `Fields` are hoisted
  out because they are the key; `Message` sits beside them and is not.
- `errs/sqlerr/corpus.go:Err.SameKey` — the comparison, and the comment saying
  what it leaves out.
- `crud/sqlfault/extract.go:carried` — the whitelist of structured fields a parser
  may read. It excludes pgconn's `File`, `Line` and `Routine`, which name
  PostgreSQL's own C source. `Detail` and `Hint` are *carried* and never read:
  they hold the offending value, and they are the two fields the server
  localises. Since phase 3 this list is also what `test/corpus/capture.go` uses
  — the corpus supplies the expectations the adapters are tested against, so a
  second copy of the rule there could stay green while the shipped one was
  broken.
- `crud/adapter/crudpgx/conflict.go:extract` — the same seven fields, spelled by name
  because that module may name `*pgconn.PgError`.
- `errs/sqlerr/classify.go:Classify` — the parser takes the whole `Err`, message
  included, and reads none of it. Passing only the key would make the invariant
  unfalsifiable: there would be nothing for a test to substitute.
- `test/corpus/cases.go:Probe.Session` — how the second capture is provoked. The
  locale has to be set on the same session as the statement, on a handle of the
  probe's own, or it outlives the probe and the *next* case is recorded in
  Russian. That happened on the first run.

## Proven by

- `TestTheCorpusStillDescribesTheseServers` in
  `test/integration/corpus_test.go` — asserts the key across four live engines
  and does not assert the text.
- `TestSameKeySeparatesTwoCapturesThatWouldClassifyDifferently` in
  `errs/sqlerr/corpus_test.go` — the comparator the guard above rests on, and
  the only thing that pins the omission of the message: the localised twin and
  the English capture have to read as one key, with their differing sentences
  asserted first so the leg cannot go vacuous. Its negatives move the SQLSTATE,
  the number, the driver type and one field name in turn, each with the
  precondition that the rest of the pair did not move. Without it a comparator
  narrowed to the driver type alone leaves the live guard green while MySQL
  answers a CHECK under a new SQLSTATE.
- `errs/sqlerr/testdata/corpus/mysql.json` and `mariadb.json` — the two files
  are the evidence: identical keys for `unique`, different sentences.

- `TestNothingInExtractionOrClassificationReadsMessageDetailOrHint` in
  `crud/sqlfault/classify_test.go` — the **extraction** half, which nothing tested
  before phase 3: the fault built from a driver error and the fault built from
  the same error with `Message`, `Detail` and `Hint` replaced by unrelated text
  agree in code, kind, source and every `Detail` field but the driver error
  itself. Its control asserts each substituted string was non-empty on the
  fixture and differs from its replacement, or it compares two identical inputs.
- `TestAParserAnswersTheSameWhateverTheServerSaid` in
  `errs/sqlerr/classify_test.go` — **the invariant**. Every case on every engine
  is classified as captured, with the message replaced by a Russian sentence,
  with the message emptied, and with `Detail` and `Hint` replaced by another
  engine's text; all four verdicts must agree, code, source and all. It reaches
  PostgreSQL and SQLite, which cannot produce a localised twin, and it is what
  fails the day somebody reads `Detail` to tell `foreign_key` from `restrict`.
  Its control is that every engine classified at least one case as captured: a
  refusal agrees with a refusal, so without it a parser answering false to
  everything satisfies every comparison the test makes.
- `TestTheSameViolationInAnotherLocaleClassifiesIdentically` in the same file —
  **the evidence** that the invariant is about something real. The corpus's
  `unique_in_another_locale` case is a duplicate key captured from a server
  answering in Russian, and it classifies as the English one does. Its three
  controls: the twin's message must differ from the plain case's; at least two
  engines must have a differing twin, so an `lc_messages` setting that quietly
  stopped taking effect turns this red rather than leaving it green; and the
  English capture must itself classify, because two refusals agree as readily as
  two duplicate-key verdicts do.

## See also

[[D-046]] [[D-041]] [[D-044]] [[D-015]]
