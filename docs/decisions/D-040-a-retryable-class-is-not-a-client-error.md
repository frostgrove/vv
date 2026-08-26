# D-040 — A retryable class is not a client error

**Status:** accepted
**Invariant:** A lock timeout, a deadlock or a serialisation failure is never classified as a conflict or any other 4xx. It carries its own `Kind`, answers 503, and the framework does not retry on the caller's behalf.

## The decision

Four engine answers say *nothing you sent is wrong; try again*: SQLSTATE class
40 (serialisation failure, deadlock), PostgreSQL's `55P03` lock timeout, MySQL's
`1205`, SQLite's `SQLITE_BUSY`, and PostgreSQL's `25P02` — a transaction the
engine aborted out from under the caller after an earlier statement failed. They
get a `Kind` of their own and a 503 with `Retry-After` left to the application.

`25P02` is `errs.CodeTransactionAborted`, and its kind is `KindRetryable`. The
alternative was `KindInternal`, and it was rejected for a mechanical reason: a
`KindInternal` code must carry no default message, and
`TestTheInternalCodeHasNoDefaultMessage` rests on exactly one code having none —
a second exemption weakens the control that keeps a 500 silent by construction.
The argument against is recorded rather than dismissed: if the statement that
poisoned the transaction was a deterministic conflict, a client that retries on
the 503 loops. What makes retryable the better answer anyway is that `25P02` is
never the *first* error — the caller has already been told the truth about the
statement that failed — so the 503 says "this unit of work is over", which is
exactly right.

The framework does not retry. It classifies and hands back.

## Why

**Because 4xx tells the client to change something, and there is nothing to
change.** A 409 on a deadlock says *your write conflicts with the stored state*,
which is false — the same request succeeds unmodified a moment later. A client
that trusts the status stops retrying and surfaces a permanent error to a user
for a transient condition.

**Because it is not a 500 either.** A 500's body deliberately says nothing
([[D-015]]), so a caller cannot tell a retryable failure from a bug, and the
sensible client behaviour for the two is opposite.

**Why the framework does not retry.** A retry has to replay the whole
transaction, and the framework does not own it — [[D-009]] and [[D-027]] exist
because a caller's executor arrives through the context and may be shared with
work the repository cannot see. Replaying a statement inside somebody else's
transaction is not a retry, it is a second attempt at a broken transaction. And
on PostgreSQL a failed statement poisons the transaction (`25P02`) so the retry
cannot even run.

The argument for the other side is real and is recorded rather than dismissed:
when the repository *does* own the transaction, nobody else can see the failure
or retry it. That case is left open in `ROADMAP-errors.md` §16 and this decision
is what it has to supersede if it wins.

**Measured, not assumed.** The corpus provokes a lock timeout on all four
engines by holding a row on one connection and waiting on another whose patience
has been cut to a fraction of a second:

| | PostgreSQL | MySQL | MariaDB | SQLite |
|---|---|---|---|---|
| lock timeout | `55P03` | `HY000`/1205 | `HY000`/1205 | —/5 |
| deadlock | `40P01` | `40001`/1213 | `40001`/1213 | —/5 |
| serialisation failure | `40001` | `40001`/1213 | `40001`/1213 | —/5 |
| tx aborted | `25P02` | *unreachable* | *unreachable* | *unreachable* |

Three of the four lock timeouts are outside class 40 entirely, which is the same lesson
[[D-046]] draws: the class is not the gate. MySQL puts it in `HY000` — the state
its CHECK violations also use — so the number is what separates `1205` from
`3819`, and reading the number without the state would classify a lock timeout
as a constraint violation.

**Two more things the matrix above only shows once measured.** MySQL and MariaDB
answer a *write skew under SERIALIZABLE* with `1213` — the deadlock number —
because InnoDB's SERIALIZABLE turns a plain `SELECT` into a shared lock, so the
skew reaches the engine as a lock cycle. And SQLite answers both races with the
primary `SQLITE_BUSY` (5), the same number its lock timeout carries: it has one
writer, so a cycle cannot form. Both are recorded in the corpus's `Want` rather
than smoothed over, and both cases keep the names of the questions they ask.

**Why deadlock was not in the corpus, and what changed.** The concern was that a
corpus entry depending on scheduling regenerates differently every run. What
made it deterministic is a rendezvous between *every pair* of statements rather
than only the first — both sides finish statement N before either starts N+1, so
each side is holding its own row before either reaches for the other's. The
cycle then forms every run. `test/corpus/corpus.go:Engine.Race` is that
choreography.

What actually regenerates differently is one field: PostgreSQL's deadlock
`DETAIL` names the two backend pids and the two transaction ids. The entry is
captured with that one value replaced by a fixed marker and the field name kept,
so `SameKey` still compares what it always compared and `Save`'s byte-identical
promise holds. The cost is stated where it is paid: a PostgreSQL that stopped
emitting `Detail` on a deadlock would no longer be a finding on that one row.

## What it forbids

- Do not map class 40, `55P03`, `1205` or `SQLITE_BUSY` to `crud.ErrConflict`.
  Class 23 and class 40 are not neighbours because their numbers are close.
- Do not let them fall through to 500. A client cannot act on silence.
- Do not add a retry loop to a repository, a decorator or a binding without
  superseding this decision.
- Do not read the number without the state. On MySQL `1205` and `3819` share
  `HY000` and mean opposite things.

## Where it lives

- `crud/sqlfault/gate.go:Integrity` — where these are refused. The classification
  half of this decision is what that refusal *is*, and phase 3 moved it out of
  `crud/adapter/crudsql` without widening it: a lock timeout, a deadlock and a
  serialisation failure are still not conflicts.
- `crud/sqlfault/classify.go:Classifier.Classify` — where a retryable failure
  gets a `Fault` carrying `KindRetryable`. Phase 4 paid the rest, and
  [[D-059]] moved it one directory further out:
  `port/porthttp/errors.go:StatusFor` turns the kind into 503 and
  `port/porthttp/render.go:EnvelopeRenderer` is what attaches `Retry-After`.
- `errs/code.go:KindRetryable` — the transport class, and the five codes that
  declare it in `errs/codes.go:StandardCodes`. Not to be confused with
  `errs/sqlerr/corpus.go:KindRetryable` below, which is an untyped corpus label
  one import path away; neither is renamed, and `errs/doc.go` says so.
- `errs/code.go:CodeTransactionAborted` — `25P02`'s own code, declared
  `KindRetryable` in `errs/codes.go:StandardCodes`.
- `errs/sqlerr/corpus.go:KindRetryable` — the label the corpus carries.
- `errs/sqlerr/testdata/corpus/*.json`, cases `lock_timeout`, `deadlock`,
  `serialization_failure` and `transaction_aborted` — the evidence.
- `test/corpus/corpus.go:Engine.Contend` — how a lock timeout is provoked, and
  `test/corpus/corpus.go:Engine.Race` beside it — how a deadlock and a write
  skew are.

## Proven by

- `TestTheTwoGatesAnswerDifferentQuestions` in `crud/sqlfault/gate_test.go` — the
  sentinel-no/code-yes cell is this decision in one row: `SQLITE_BUSY` and
  PostgreSQL's `40001` classify and are not conflicts, so a retryable failure
  cannot reach a client as a 409 by being classified.
- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` in
  `test/integration/corpus_test.go` — the `lock_timeout` arm on four live
  engines asserts it is **not** a conflict, which is this decision's whole
  content until the `Kind` exists.
- `TestAnOrdinarySQLiteErrorIsStillNotAConflict` in
  `crud/adapter/crudsql/conflict_test.go` — `SQLITE_BUSY` (5) and busy-snapshot (517)
  by number.

- `TestTheRetryableCodesAreTheirOwnKind` in `errs/codes_test.go` — `deadlock`,
  `serialization_failure`, `lock_timeout`, `transaction_aborted` and
  `unavailable` all resolve to `errs.KindRetryable` in the standard vocabulary,
  with four codes of other kinds beside them as the control: without those, the
  assertion passes for a table that answers retryable to everything and both
  forbids above would look proven while neither was tested.
- `TestARetryableCaseNeverAnswersAConflictOrValidationCode` in
  `errs/sqlerr/classify_test.go` — every retryable case in every corpus,
  classified and then looked up in `StandardCodes`, must come back
  `KindRetryable`; every integrity and data case must not. It is the only test
  in that package whose expectation is not written beside the parser, so a
  consistently wrong row — `1205` answering `check`, the fourth forbid above
  verbatim — passes the corpus-`Want` test and fails here.
- `TestTheCorpusStillDescribesTheseServers` in
  `test/integration/corpus_test.go` — the redaction the deadlock entry rests on:
  a field the corpus records as varying between runs has to come back redacted
  on recapture, with the count of redacted fields walked as the control, because
  the loop is empty and green on a corpus that redacts nothing. `SameKey`
  compares field names and never their values, so a probe that lost its
  `Volatile` list breaks nothing except the diff — and the diff is the only
  reader these files have.
- `TestSavingAnUnchangedCorpusRewritesNothing` in `errs/sqlerr/corpus_test.go` —
  the other half of that promise: loading and saving the four checked-in files
  produces them byte for byte, with a corpus naming a different server as the
  control. Nothing else exercises `Save`; `make corpus` writes the files and no
  target diffs the result.

## Proven by — the class, the status and the forbid

- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` in
  `test/integration/corpus_test.go` — the `lock_timeout` arm on four live
  engines asserts it is **not** a conflict.
- `TestAnOrdinarySQLiteErrorIsStillNotAConflict` in
  `crud/adapter/crudsql/conflict_test.go` — `SQLITE_BUSY` (5) and busy-snapshot (517)
  by number.
- `TestTheRetryableCodesAreTheirOwnKind` — the `Kind`, which phase 1 shipped.
- `TestARetryableFailureIsA503WithRetryAfter` in `write_edge_test.go` in all three
  HTTP bindings — the status and the header. `errs` still declares no status
  table: a `Kind` → status map there would put an HTTP type in the
  transport-neutral half, which [[D-045]] forbids, so the table is
  `port/porthttp.StatusFor` ([[D-059]] moved it there from `crud/http/crudhttp`)
  and [[D-049]] decides which of it and the sentinel wins.
- `TestA503AdvertisesTheRetryAfterTheConsumerSet` in
  `port/porthttp/render_options_test.go` — the same header from the other side:
  the number `WithRetryAfter` sets is the number on the wire, with the
  unconfigured `DefaultRetryAfter` as the control, `WithRetryAfter(0)` writing
  no header at all, and a 409 from the same renderer carrying none — so the
  header is tied to the status rather than to the renderer.
- `TestARetryableCaseNeverAnswersAConflictOrValidationCode` — the forbid, as a
  test rather than a sentence.

## See also

[[D-046]] [[D-015]] [[D-009]] [[D-027]]
