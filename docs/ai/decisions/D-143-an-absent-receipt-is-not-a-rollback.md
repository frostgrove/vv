# D-143 — An absent receipt is not a rollback, and the third answer is the point

**Status:** accepted
**Invariant:** `receipt.Resolve` has **four** standings and `receipt.Claim` has
**three**, and the difference is one state: an **absent** row, which a resolver
on a second connection cannot tell from a writing transaction that is still open.
That state is named `Unresolved` rather than guessed at. No lock closes it, no
fourth standing proves a rollback, and retention bounds the window through a
published horizon rather than by letting a swept row read as one that never
existed.

## The decision

### Why a resolve has four answers and a claim has three

A claim holds the primary-key index. PostgreSQL's speculative insertion makes
`INSERT … ON CONFLICT DO NOTHING` **wait** on a conflicting uncommitted tuple and
then insert or not on that transaction's outcome, so by the time a claim has an
answer the other transaction has resolved. A claim therefore never sees
`Unresolved` — its three are `Recorded`, `Repeated` and `Collided`.

A resolve writes nothing and takes no lock, so it sees whatever is committed at
the instant it looks. Its four are `Found`, `Incomplete`, `Unresolved` and
`Expired`. It never sees `Recorded`, because it records nothing.

They are **two enums** and not one of seven members, because one enum would let
each door return the other's: a `Verdict` of `Unresolved` and a `Standing` of
`Recorded` would both compile and neither is reachable.

### What `Unresolved` means, in PostgreSQL terms, and why no lock closes it

A resolver on a second connection sees no row for the key. Two different worlds
produce that reading and they are indistinguishable:

- the writing transaction **rolled back**, so the row never committed; or
- the writing transaction is **still open**, and its uncommitted row is invisible
  to every other snapshot.

There is nothing to lock. `SELECT … FOR SHARE` blocks on a *row*, and in the
second world there is no row in this snapshot to block on — the statement returns
zero rows immediately. `pg_advisory_xact_lock` over the key would work only if
the writer took one, which would put a lock into the claim's happy path for the
benefit of a resolver that may never run. **So `Unresolved` is that state named,
and it is the appendix's own sentence:** *«Отсутствующая квитанция не доказывает
rollback, пока исходная транзакция не разрешилась»*.

### What a caller does with it

It does **not** re-issue the command. A fresh `Load`, a fresh decision and a
fresh `Append` is a *different* operation under the same key: the state it
decided from is not the state the first attempt decided from, so its fingerprint
differs, and if the first attempt does commit the result is one key covering two
appends with nothing to catch the second write.

What it does is report the operation as **in flight** and resolve again later.
"Later" has a bound, and it is an **inherited** lever rather than an invented one:
`idle_in_transaction_session_timeout` on the application role is what makes an
abandoned transaction end, and it is the deployment's setting. This framework
adds no second timeout of its own, has no session to time out, and publishes no
knob that would look like one.

### Why a claim answers `ErrIncomplete` rather than a fourth verdict

A **present** row whose claim committed without its completion is a different
thing from an absent one. `Unresolved` is a row that may still arrive;
`Incomplete` is a row that never will — its claim's transaction committed, so
nothing is going to write the range onto it now.

At the claim door that state is `ErrIncomplete` and carries **no verdict at
all**, because a state nothing may be concluded from is a refusal and not an
answer. A retry told *"already done"* out of a row whose range is `0..0` has
reported a success that may never have happened; and `0..0` is also the honest
range of a decision that yielded no changes, which is why the discriminator is
`Receipt.Complete` and never the range.

At the resolve door the same row is the standing `Incomplete` — a **defect
report**, addressed to an operator, about a caller that reached its commit
without completing.

### Why a resolve refuses inside the writing transaction

`Resolve` is `ErrSpec` when a transaction of the store's **or** of the ledger's
is bound to the context. Inside the claiming unit a resolve reads its own
uncommitted row and answers `Found` for an operation that can still roll back —
and "resolve first, then decide, in one unit" is an ordinary retry shape somebody
writes rather than an exotic one. `ResolveSpec.Store` exists for exactly that
question and for no other: it is asked whether a transaction of the store's is
bound, and nothing else about it is read.

It is the mirror of the claim's placement rule — a claim and a completion refuse
**outside** one transaction, and a resolve refuses **inside** any — and a
question that cannot be answered is refused rather than read as a no: a store or
a ledger that reports something bound which is not a transaction has told this
call the one thing it cannot proceed past.

### Retention bounds the window, through a horizon and not through silence

A sweep is the application's; nothing in this framework prunes. But a swept row
is an absent row, and an absent row read without a horizon is `Unresolved` for
ever — the mechanism's window would be unbounded and its answer would decay into
"ask again later" with no later that helps.

`Ledger.Horizon` is the bound: **an instant at or before the oldest row this
ledger still answers for.** An absent row whose key was minted *before* the
horizon is `Expired` — it may have existed and been swept — and one minted at or
after it is `Unresolved`.

The asymmetry of the contract is deliberate. Answering an instant **older** than
the truth is conformant and costs a caller one more `Unresolved`; answering a
**newer** one is not conformant, because it reads a row that was swept as one
that never existed. That is why the two spellings the contract names are
`MIN(recorded_at)` and `now() - retention`, and why `MAX(recorded_at)` is one of
the four ledger defects the live suite injects.

A ledger that answers the **zero** instant claims to hold everything for ever,
which is exactly what a table with no sweep is — so the zero horizon is not a
refusal.

### Which clock each instant comes from

Three instants are in play and they come from three places, which is stated
because a reader who assumes one clock draws a wrong conclusion from the
comparison.

| Instant | Clock | Why |
|---|---|---|
| `Receipt.RecordedAt` | the ledger's database, `statement_timestamp()` | bound as a parameter it would be the writer's process clock, and a fleet's clocks disagree; then a row written by a host two minutes fast is never swept on schedule and the horizon it is compared against is measured on a different scale |
| `Ledger.Horizon` | the ledger's database, in SQL | the same clock the rows are on, which is what makes the comparison against `RecordedAt` meaningful at all |
| `ResolveSpec.Issued` | the **caller's** own | it is the instant the caller minted its key, and no database saw that happen |

So exactly **one** cross-clock comparison exists — `Issued` against `Horizon` —
and its error term is the skew between the resolving host and the database.

**That error term is tolerable and the argument is that it decides only between
two non-conclusions.** A caller clock behind the database's answers `Expired` for
an operation that is in flight; one ahead answers `Unresolved` for a row that was
swept. Neither is a false `Found` and neither is a false *"it did not happen"*.

`Issued` is **optional** and its zero value gets `Unresolved` rather than a
refusal. An HTTP handler holding a retried idempotency key has the key and not
the instant, and a refusal there sends that caller to `time.Now()` — which is
after every horizon, disables `Expired` for ever and reports nothing to anybody.
Absent, `Resolution.Horizon` carries the ledger's instant so the caller can make
the comparison with a date it does have.

### The fourth standing that was designed and refused

A proof of rollback **is** available in PostgreSQL, and it is refused with its
cost stated so it is not re-proposed.

The shape: the claim records `pg_current_xact_id()` on the row, and the resolver
compares it against `pg_snapshot_xmin(pg_current_snapshot())`. A transaction id
below the snapshot's xmin has finished, so an absent row whose writer's id is
below xmin is a **rollback** — a real fourth standing, provable. `eventpg`'s log
walk already makes this walk, so it is not exotic here.

It is refused for four reasons:

1. **The row is the evidence, and there is no row.** The transaction id would
   have to be recorded *somewhere else* — a second table, written by the same
   transaction, which has the identical absent-row problem one level down.
2. **`xmin` is cluster-wide.** One session idle in transaction anywhere in the
   cluster holds it down, so the proof stalls for every key at once and for a
   reason no reader of this subsystem can see. `docs/modules/en/eventpg.md`
   already documents that stall for the log walk; a second mechanism resting on
   the same floor doubles the blast radius of one idle session.
3. **It puts a PostgreSQL-only call into a contract that is not PostgreSQL's.**
   `Ledger` is any SQL database the application chose. A standing only one engine
   can answer is a standing a third-party ledger cannot implement.
4. **The caller's action does not change.** `Unresolved` and a proved rollback
   both mean *do not re-issue this operation under this key*; what differs is
   only how soon the caller may stop reporting "in flight". That is a latency
   improvement on an error path, bought with a cluster-wide dependency and a
   second durable table.

## What it forbids

- Do not turn an absent receipt into a rollback, in any standing, on any clock.
- Do not take a lock to close the window, and do not add a timeout of this
  framework's own beside `idle_in_transaction_session_timeout`.
- Do not merge `Verdict` and `Standing`, and do not add `Unresolved` to a claim.
- Do not let `Resolve` run inside the claiming transaction, and do not read
  anything of `ResolveSpec.Store` beyond the transaction question.
- Do not bind `recorded_at` as a parameter, and do not compute a horizon from a
  process clock.
- Do not make `ResolveSpec.Issued` required, and do not refuse the zero instant.
- Do not publish a horizon later than the oldest row the ledger answers for.

## Where it lives

- `event/receipt/resolve.go` — `Standing`, `Resolve`, `standingOfAnAbsentRow`,
  `outsideEveryTransaction`, `findAnswered`.
- `event/receipt/claim.go` — the three verdicts and the `ErrIncomplete` refusal.
- `event/receipt/errors.go` — `ErrIncomplete`, whose comment carries the
  absent-versus-present distinction.
- `event/receipt/receipt.go` — `Ledger.Horizon` and `Receipt.RecordedAt`.
- `_examples/event-receipts/main.go` — the SQL-side horizon and the sweep.

## Proven by

- `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` — the four standings.
- `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` — a second
  session held idle in transaction, asserting the open case and the rolled-back
  case answer the same thing.
- `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` — both horizon spellings,
  the zero `Issued`, and the two directions of skew.
- `TestThePublishedClaimBindsNoInstantAsAParameter` — the instant is the
  database's, read out of the statement the example publishes.
- `TestARepeatIsAnsweredOnlyFromACompleteRow` — the `ErrIncomplete` arm.
- `TestAnUnresolvedClaimRefusesItsRetry` — a claim that reached its commit with
  nobody resolving it.
- `TestAResolveInsideTheWritingTransactionIsRefused` — with the
  after-the-commit control.
- `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` — the `optimistic
  horizon` and `dirty find` defects each break the case that names them.

## See also

[[D-126]] [[D-128]] [[D-142]] [[FL-043]] [[UC-037]]
