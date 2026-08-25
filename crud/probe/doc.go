// Package probe turns a database's one-violation-at-a-time answer into every
// violation the payload would have caused.
//
// A failed write comes back classified: one code, one constraint, one message
// ([[FL-014]]). The client fixes it, posts again, and is told about the next
// one. [Full] issues a single extra statement — one boolean column per
// constraint the write could have broken — and reports the rest.
//
// Everything here is subordinate to the statement that actually failed
// ([[D-042]]). The index is the truth and the probe is advice.
//
// # What it probes, and what it refuses to
//
// Three codes, and no others: unique, foreign_key and restrict.
//
//   - unique — EXISTS over this table, one term per unique key the write's own
//     columns touch. An update excludes its own row.
//   - foreign_key — NOT EXISTS over the parent, guarded by every referencing
//     column being non-null.
//   - restrict — the inbound direction. An update that changes a column another
//     table's foreign key points at under ON UPDATE RESTRICT or NO ACTION, with
//     children that still point at the old value. It needs the inbound half of
//     the schema, which no lookup on [catalog.Catalog] can express, so a catalog
//     that is not a [catalog.Referrers] simply produces no restrict terms.
//
// **CHECK constraints are not evaluated.** [catalog.Constraint] carries a CHECK
// only as Definition, and the shape differs per engine — pg_get_constraintdef
// hands back `CHECK ((qty > 0))`, information_schema.CHECK_CONSTRAINTS the bare
// clause, and SQLite nothing at all. Recovering the expression from that text is
// DDL parsing, which [[D-041]] forbids in as many words, and evaluating one
// against a synthesised candidate row needs the defaults and generated-column
// expressions no catalog here holds. Every gap in that is a chance to report a
// violation the server would not have raised, which is the one direction
// [[D-042]] rules out. A later phase that wants CHECK should add a parsed
// Expression to the catalog, filled by pg_get_expr and CHECK_CLAUSE, and parse
// nothing itself.
//
// **NOT NULL, length, range and enum membership are not checked** — not in Go
// and not in SQL either. MySQL makes the argument: under STRICT_TRANS_TABLES a
// too-long value is an error, and without it the same value is a warning and a
// silent truncation. Any rule this package re-derived would be right on one
// deployment and wrong on another it cannot see.
//
// **There is no pre-flight mode.** Probing before the write costs a query on
// every happy-path request, and the TOCTOU window between the check and the
// insert means a clean answer is a lie under concurrency. See [[D-042]].
//
// # The one Go-side check
//
// Intra-payload duplicates in a batch: two rows of the same insert carrying the
// same email. The database reports one, both are wrong, and finding them takes
// a map rather than a statement — see dup.go. It is unambiguously correct
// because it is a fact about the payload and not about the database. It is also
// only ever narrowing: a collation equates *more* values than byte equality
// does, never fewer, so two rows this finds equal are equal to the server too.
//
// # The transaction problem
//
// PostgreSQL aborts the whole transaction on a constraint error, and nothing
// runs until ROLLBACK or ROLLBACK TO SAVEPOINT. A savepoint cannot be taken
// after the fact, so the decorator takes it *before* the write and this package
// is told whether the transaction was restored ([Request.Recovered]).
//
//	the write ran…                     | poisons (PostgreSQL) | statement-scoped (MySQL, MariaDB, SQLite)
//	-----------------------------------+----------------------+------------------------------------------
//	outside any transaction            | Full                 | Full
//	inside a transaction vv opened     | Simple, unless        | Full
//	                                   | WithSavepoints()     |
//	inside a foreign transaction       | Simple, always       | Full
//
// A foreign transaction is never given a savepoint. An ent or gorm transaction
// has its own savepoint stack and its own expectations about what runs inside
// it, and ROLLBACK TO SAVEPOINT in the middle of somebody else's unit of work
// can discard work its owner has not finished with.
//
// Which side of that table a dialect is on comes from [crud.StatementRollback],
// not from its name — [[D-019]] forbids a name check standing in for a dialect
// check and this package is not exempt.
//
// # The caps, with numbers
//
// A cap without a number is not a cap.
//
//   - [DefaultMaxConstraints] = 16 terms per request. One term per constraint,
//     and relevance already narrows by written columns; sixteen *relevant*
//     constraints on one write is already pathological.
//   - [DefaultMaxRows] = 50 rows per batch. A batch probe is one flat statement
//     of one term per constraint per row, so 50 × 16 is 800 columns of one
//     result row — inside PostgreSQL's 1664-column limit with room to spare, and
//     it bounds the hostile 10 000-row batch to a fixed cost.
//   - [DefaultTimeout] = 250ms around the probe statement only. The write has
//     already failed and the client is waiting.
//   - [DefaultMaxSavepoints] = 32 per transaction. PostgreSQL's subxid cache
//     overflows at 64 per top-level transaction, and the overflow is not a round
//     trip — it forces pg_subtrans lookups on every reader in the cluster. Half
//     of the cliff.
//
// The catalog load has no cap here, deliberately: it runs once at declaration on
// a context the application owns, and a default timeout would turn a slow but
// healthy start-up into the fatal refusal [[D-041]] makes it.
//
// Hitting any of them sets Partial on the fault. A partial answer presented as
// complete is worse than the one violation it replaced.
//
// # The oracle
//
// The probe queries rows the caller may not be allowed to see, and a
// unique-violation response reveals that a value exists. Four controls, none of
// which closes it:
//
//   - the value never reaches the payload unless [WithValues] says so;
//   - [Skip] takes a constraint out by name;
//   - [WithScope] narrows the unique terms with the same predicate the
//     security policy narrows reads with;
//   - [CodeOnly] drops the path and keeps the code.
//
// [WithScope] narrows the *unique* terms and nothing else, and the limit is not
// an oversight. A foreign-key term reads the parent table and a restrict term
// reads the child; the model's own scope predicate names neither, and a
// predicate over the wrong table would not compile. Where that matters, [Skip]
// is the control.
//
// # Determinism
//
// Terms are produced in catalog order and rows in payload order, and the answer
// is sorted with [errs.SortViolations] before it is returned. The same failing
// request twice produces the same list — [[D-014]] one layer up.
package probe
