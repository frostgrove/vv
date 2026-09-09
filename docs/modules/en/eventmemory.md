# event/eventmemory — a complete event store, in memory

```go
import "github.com/frostgrove/vv/event/eventmemory"
```

**Module:** in the root module — standard library and `event` only
· **Depends on:** [event](event.md) · **Depended on by:** nothing

`eventmemory` is a full implementation of the store contract and not a test
double. Expected-version admission, dense versions, strictly increasing
positions, one-append atomicity and transactions with staged writes all work the
way a database-backed store must make them work. It is what earns the seam a
second implementation is measured against ([[D-121]]), and it is what a domain
test runs against with no database at all.

Nothing survives the process, which is what `Persistence: Unsupported` says.

---

## What you get

| | |
|---|---|
| `NewLog(LogSpec)` | a `*Log` — the backing. `MaxPayload` and `MaxKey`, each defaulting and each bounded by the framework's ceiling |
| `New(Spec)` | a `*Store` over a log: `Clock`, `MaxBatch`, `StreamPage`, `MaxRead` |
| `Store.Begin(ctx)` | a `*Tx` of this log's |
| `WithTransaction(ctx, tx)` | the context that carries it |
| `Tx.Commit(ctx)` · `Tx.Rollback(ctx)` | the two ends. Neither is called by the framework |
| `Store.Check(ctx)` | a readiness answer: `event.ErrClosed` once closed |
| `NewCheckpoints(CheckpointSpec)` | a `*Checkpoints` over the same log — the checkpoint contract with no database |
| `Checkpoints.Begin(ctx)` | a `*Tx` of the log, opened through the checkpoint store, so a consumer that records progress and writes nothing to the log needs no `*Store` to open a unit of work |

```go
log, err := eventmemory.NewLog(eventmemory.LogSpec{})
store, err := eventmemory.New(eventmemory.Spec{Log: log})

repo, err := event.Bind(event.Open(store), Account)
```

Every number is optional and every number is checked. A zero takes the default —
`MaxPayload` 64 KiB, `MaxKey` 512, `MaxBatch` 64, and a page of 256 or whatever
the framework's resident rule allows at the payload bound, whichever is smaller.
A negative is refused, and so is anything above the framework's ceiling.

## The log is the backing, and the store is not

The two numbers the *data* depends on — `MaxPayload` and `MaxKey` — live on the
`Log`. The operational numbers — the batch size and the two page sizes — live on
the `Store`. So two store values over one log are one store and cannot disagree
about which keys and which payloads are writable, while a request-scoped store
value may still page differently from a projector's.

That is also what a restart is here: a second `Store` over the same `Log` reads
everything the first one wrote, and a cursor minted by either resumes through
both, because the log names itself in every cursor rather than the store value
doing so.

## Transactions, claims and where a conflict comes from

A transaction stages its records and takes a claim on every stream it writes to.
Admission is against the committed version **plus what this transaction has
already staged**, so a second append inside one transaction is admitted at the
version the first produced rather than at the committed one.

A conflict is therefore reported from `Append` — never from the commit. That is
deliberate and it is the shape a SQL store has too: the unique index reports it
at the insert, and that is where a caller branches on it.

A claim is refused at once rather than waited for. Two transactions taking two
streams in opposite orders refuse each other instead of deadlocking, which is a
better trade in a process than a deadlock detector. It has two consequences worth
knowing:

- a transaction held open across a network call **starves every other writer of
  its streams**;
- a transaction that is never committed and never rolled back — a panicked
  goroutine, a forgotten `defer`, an early `return` — holds its claims until the
  runtime collects it. A claim names its transaction weakly, so a `*Tx` nothing
  can reach any more holds nothing: it can never be committed or appended to, its
  staged records will never be published, and the next append to one of its
  streams drops the claim and is admitted. Until that collection happens the
  appends are conflicts, so an abandoned transaction is a stall and not a brick.

  The reclamation is reachability and nothing else, so whatever still names the
  transaction keeps its claims: the `*Tx` itself, and a context carrying it that
  outlives the request. A **commit receipt is not one of them** — the
  `event.Authority` in it names this store's own name for the transaction rather
  than the `*Tx`, because a receipt is a value a caller is meant to keep, in an
  audit buffer or an outbox row, and a store whose transaction is a database
  handle loses nothing by keeping it.

A read issued inside a transaction sees that transaction's own staged appends;
the log walk does not, its own transaction included, because a position is
assigned at commit. Both are this store's answers to questions
`event.Log.ReadAll` and `event.Envelope.Position` leave to the store — a staged
envelope here reads back at position **0**, and a store that draws positions
from a sequence answers a real one. Do not persist a cursor from a log walk
taken inside a write transaction.

`ctx.Value` is the caller's own code — a decorator, a chain of them — so every
door looks the transaction up **before** it takes the log's lock and asks only
whether it is still live inside. A caller whose `Value` takes a lock of its own
would otherwise hang every reader and writer of the log.

## The checkpoint store is the log's too

`NewCheckpoints(CheckpointSpec{Log: log})` answers the `event.Checkpoints`
contract over the same `*Log`, and the rows live **on the log** rather than on
the value — so two `Checkpoints` values over one log are one checkpoint store,
exactly as two `Store` values over one log are one store, and a restart resumes
through a value that did not exist when the row was written.

It joins the log's own ambient transaction, which is what lets a consumer prove
the one-unit path with no database at all: a save inside a `*Tx` is staged in a
second staging area beside the appends, the fence is evaluated when it is staged
**and again at the commit**, and a rollback discards it. A row that moved between
the stage and the commit is therefore an error rather than a save that overwrote
a winner.

It says `Transactions: Supported` and `Persistence: Unsupported`, so
`eventtest.RunCheckpoints` certifies eleven of its twelve sections and reports
`durability: not certified` with this store's own reason. That pair of facts —
eleven passed, one declined — is asserted rather than left to a reader of the
log.

```go
checkpoints, err := eventmemory.NewCheckpoints(eventmemory.CheckpointSpec{Log: log})
tracker, err := event.Track(checkpoints, "balances")
```

## Positions, and what a rollback burns

A position is assigned inside the one critical section that publishes, so commit
order is position order and the newest position is the watermark. A rollback
discards the staged records and advances the counter by the number it staged:
those positions are burned, the gap stays, and nothing is ever reissued. A
checkpoint draws no position, so the arithmetic reads the appends' staging area
and not the saves'. Gaps in
the positions are normal and every consumer of the log already has to tolerate
them.

## Concurrency

A `*Store` and a `*Log` are safe from any number of goroutines, which is what the
store contract requires. A `*Tx` may be used from more than one goroutine, and a
context carries at most one transaction **per log**: a transaction of another log
neither shadows this store's own nor is mistaken for it, and an operation on a
transaction another goroutine has just finished is refused rather than silently
run on autocommit.

The clock is yours: `Spec.Clock` is retained and called on every append from
every goroutine that appends, so it must be safe for that. Its panic is not
recovered — a clock has no error channel for a panic to be a second spelling of.

## What it says about itself

| Capability | Answer |
|---|---|
| `Transactions` | `Supported` |
| `Persistence` | `Unsupported` — nothing survives the process |
| `MonotoneVisibility` | `Supported` — positions are assigned in commit order |
| `SharedBacking` | `Supported` — two store values over one log |

`Close` is idempotent, decides nothing and closes nothing it did not open: the
log was constructed by the composition root and is shared with every other store
over it, and staged work belongs to the transaction that staged it.

## See also

- [event](event.md) — the vocabulary, the seam and the refusals
- [projection](projection.md) — the consumer that records through this store
- [eventtest](eventtest.md) — the suite this store is certified by
- [[D-121]] · [[D-128]] · [[D-133]] · [[FL-036]] · [[FL-038]] · [[UC-032]]
