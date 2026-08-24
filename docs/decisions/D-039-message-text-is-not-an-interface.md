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
| CHECK | `Check constraint 'cp_pos' is violated.` | ``CONSTRAINT `cp_pos` failed for `vv`.`cp_parent` `` |
| bad type | `… for column 'num' at row 1` | ``… for column `vv`.`cp_parent`.`num` at row 1`` |

The index name is table-prefixed on MySQL and bare on MariaDB. A regex written
against one silently extracts the wrong identifier from the other.

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
- `test/corpus/capture.go:carried` — the whitelist of structured fields a parser
  may read. It excludes pgconn's `File`, `Line` and `Routine`, which name
  PostgreSQL's own C source.

## Proven by

- `TestTheCorpusStillDescribesTheseServers` in
  `test/integration/corpus_test.go` — asserts the key across four live engines
  and does not assert the text.
- `errs/sqlerr/testdata/corpus/mysql.json` and `mariadb.json` — the two files
  are the evidence: identical keys for `unique`, different sentences.

## Proven by (owed)

- Phase 2 owes the parser test that pins this rather than merely respecting it:
  the same violation captured with `lc_messages` set to a non-English locale
  classifies identically. Nothing else tests this invariant directly.

## See also

[[D-046]] [[D-041]] [[D-044]] [[D-015]]
