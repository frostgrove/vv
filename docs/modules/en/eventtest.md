# event/eventtest — the store contract, as a suite your store runs

```go
import "github.com/frostgrove/vv/event/eventtest"
```

**Module:** in the root module — standard library, `testing` and the three
packages whose contracts it certifies · **Depends on:** [event](event.md) ·
[projection](projection.md) · [receipt](receipt.md) · **Depended on by:** nothing

Writing an event store is not the hard part. Knowing whether it is *correct* is,
because every way of getting it wrong is silent: a short page truncates a
history, a reused position makes a consumer skip an event it never saw, a
last-write-wins append loses a decision, a pooled buffer rewrites a value the
application already holds. None of those produces an error anywhere.

`eventtest` is the contract written as an executable suite. You supply a
`Factory` and call `Run`; the suite dispatches twenty named sections and reports
each of them in one of three words.

---

## What you get

| | |
|---|---|
| `Run(t, factory)` | the whole suite, one `t.Run` per section |
| `Factory` | `New`, `Begin`, `Sibling`, `Fail`, `Tail`, `Unparsable`, `Window` |
| `Tx` | `Commit(ctx)` · `Rollback(ctx)` — what the suite requires of the transaction a factory begins |
| `Keys(t, aggregate, ids…)` | your identity mapper's injectivity, over your own identities |
| `Families(t, declarations…)` | one family per aggregate, across your own declarations |
| `RunCheckpoints(t, factory)` | the fourteen-section suite for an `event.Checkpoints`, one `t.Run` per section |
| `CheckpointFactory` | `New`, `Begin`, `Sibling`, `Cursor`, `Instant`, `Window` |
| `RunLedger(t, factory)` | the seven-section suite for a `receipt.Ledger` |
| `LedgerFactory` | `New`, `Begin`, `Window` |
| `RunGenerations(t, factory)` | the five-section suite for a `projection.Generations` |
| `GenerationsFactory` | `New`, `Begin`, `Closes`, `Window` |
| `RunPark(t, factory)` | the six-section suite for a `projection.Park` |
| `ParkFactory` | `New`, `Begin`, `Sequences`, `Letters`, `Window` |
| `RoundTrip(t, fact, byRevision…)` | your payload survives its own codec, per retained revision |

```go
func TestMyStoreSatisfiesTheContract(t *testing.T) {
	eventtest.Run(t, eventtest.Factory{
		New: func(t *testing.T) event.Store {
			store := open(t)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
			tx := begin(t, s)
			return withTransaction(ctx, tx), tx
		},
	})
}
```

## Three words, never two

| Word | What it means |
|---|---|
| `passed` | the section ran and the store did what the contract says |
| `not certified` | the store does not claim the capability, or supplied no hook to drive it — so nothing was demonstrated either way |
| `failed` | the store did something the contract forbids, and the reason names it |

A section nobody could run is **not** a pass. That distinction is the reason the
suite exists in this shape: a store that quietly skipped half of it would look
identical to one that passed all of it.

There is a fourth state and it is never printed as a pass: a section that
reported no verdict at all. A factory hook that calls `t.Fatal` leaves the
subtest through `runtime.Goexit`, so nothing inside the section ever returns —
and the safe default for a verdict nobody computed is not the one word that means
the store is correct.

## A hook that is missing is not a capability that is not claimed

A store that **claims** a capability and supplies no hook for it fails the run
before any section starts. A store must not be able to claim a capability and
then avoid being tested on it.

| Hook | Required when |
|---|---|
| `New` | always — the factory constructs, and the suite never does |
| `Begin` | the store claims `Transactions` |
| `Sibling` | the store claims `SharedBacking` |
| `Fail` | never; without it the store-failure section is not certified |
| `Tail` | never; without it every walking section reads to the end itself |
| `Unparsable` | never; without it the unreadable-cursor clause is not certified |

`Window` is the deadline every section runs under. A store whose operations are a
network away, or that waits for a competing transaction rather than refusing at
once, sets its own here rather than being reported failed for the wait.

## The fourteen checkpoint sections

A checkpoint store is the second seam this package certifies, and it is certified
the same way — the same three words, the same anti-vacuity rules, the same
"a hook that is missing is not a capability that is not claimed".

`binding` · `absence` · `round trip` · `fence` · `forget` · `names` · `bounds` ·
`refusal classes` · `lifecycle` · `concurrency` · `transactions` · `durability` ·
`topology` · `topology handoff`

`transactions` and `topology handoff` run only for a store claiming
`Transactions`, and `durability` only for one claiming `Persistence`; a store
claiming either and supplying no hook fails the run before a section starts. So
`eventmemory.Checkpoints` is certified on thirteen and reports
`durability: not certified`, and live `eventpg.Checkpoints` passes fourteen.

| Hook | Required when |
|---|---|
| `New` | always. It is called several times per section and a later value must not reset what an earlier one wrote — a section reads back through a second value what a first one wrote |
| `Begin` | the store claims `Transactions` |
| `Sibling` | the store claims `Persistence` — a second value over the same backing, which is what a restart is |
| `Cursor` | always. A cursor **of the log this store records against**, real rather than a literal the suite invented, and two consecutive calls must answer two different cursors: a section that cannot tell a stale answer from a fresh one certifies a store that never wrote |
| `Instant` | never; it is the grain the store's own instant column keeps, and without it the suite measures at its own |

Four of the fourteen are worth naming here because they are obligations a
third-party store might not expect. **`round trip` requires the cursor that went
in to be the one that comes back, byte for byte, through a second value** — a
store that re-encodes it is refused there, and a projection's settlement compares
cursors to tell its own save from a second instance's. And **`bounds` requires
the empty cursor to be refused and no row to move**: the empty cursor is the
origin of a log, so a row carrying one at a live advance restarts a consumer at
the beginning against a live destination.

The two that arrived with partitioned projections are what a split rests on.
**`topology` is mandatory**: a cursor written under one projection name must read
back **unchanged under another** — that is the whole of how a parent hands its
position to its children — and a save at advance 1 over a live row must be
refused by the store's own fence, not only by the tracker's, because a child row
written over one that is already recording is a partition silently adopted.
**`topology handoff`** rides on the `Transactions` claim and asks that a `Load`,
two `Save`s at advance 1 and a `Forget` inside one caller-opened transaction are
all or nothing.

That is **three obligations**, and only the first two were reachable before
partitioned projections existed. **A store certified against the phase-3 suite
may go red on the third**, and that is the suite widening rather than the store
regressing: a `Checkpoints` that claims `Transactions` and commits its writes one
at a time passed everything the earlier suite asked and leaves a split half-made
— a child row beside a live parent, two writers over one share of the log, each
with its own fence and its own watermark. The widening is written here and in the
package doc because the release baseline cannot see it: `docs/api/surface.md`
records a signature and `RunCheckpoints` did not change one.

```go
func TestMyCheckpointsSatisfyTheContract(t *testing.T) {
	eventtest.RunCheckpoints(t, eventtest.CheckpointFactory{
		New:    func(t *testing.T) event.Checkpoints { return open(t) },
		Cursor: func(t *testing.T) event.Cursor { return mint(t) },
	})
}
```

## The three interfaces your application implements

A store and a checkpoint store are ours to certify because we ship two of each.
`receipt.Ledger`, `projection.Generations` and `projection.Park` are the other
half of this extension and **nothing in this repository implements them**: the
row, the table, the statements and the sweep are the application's. Each of the
three carries an obligation that is stated in prose, enforced by no signature and
checkable at run time by nothing — and each of those obligations, got wrong, is
silent.

| Run | Certifies | The obligation nothing else can check |
|---|---|---|
| `RunLedger` | `receipt.Ledger` | the claim is `INSERT … ON CONFLICT DO NOTHING` and **then** a `SELECT`, in that order and in one unit. Reversed, two callers both answer `Recorded` and both append under one operation key |
| `RunGenerations` | `projection.Generations` | `Active` is a **locking** read. Plain, a cutover commits between that read and the unit's commit, and a retired generation stages the effect anyway |
| `RunPark` | `projection.Park` | which of the four methods needs the caller's unit, and what `Holds` answers **outside** one — where a wait asks it |

`RunLedger` reports `claim` · `repeat` · `claim order` · `unit of work` ·
`completion` · `horizon` · `transaction`. `RunGenerations` reports
`ungenerated` · `activation` · `fenced activation` · `unit of work` ·
`locking read`. `RunPark` reports `outside a unit` · `inside the unit` ·
`committed state` · `counts` · `identity` · `bounds`. The three words are the
store suite's three, and so are the anti-vacuity rules.

```go
func TestMyLedgerSatisfiesTheContract(t *testing.T) {
	eventtest.RunLedger(t, eventtest.LedgerFactory{
		New: func(t *testing.T) receipt.Ledger { return open(t) },
		Begin: func(t *testing.T, ctx context.Context, l receipt.Ledger) (context.Context, eventtest.Tx) {
			tx := begin(t)
			return withTransaction(ctx, tx), tx
		},
	})
}
```

**`Begin` is required by all three and is called twice at once.** What the
ledger's `claim order` section and the ownership row's `locking read` section
measure is one unit **waiting** behind another, so a factory whose units come
from a pool of one blocks until the section window expires.

**Two declarations, and neither may be left at its zero value.**
`GenerationsFactory.Closes` says what closes the window between the ownership
read and the unit's commit — `eventtest.LockingRead` or
`eventtest.SerializableUnit` — and stating neither **fails the run before any
section starts**. Declaring `SerializableUnit` reports `locking read` as *not
certified*, with the reason: at that level the abort that closes the window comes
from the cutover reading the checkpoint rows the staging unit writes, and a
harness holding two methods and no checkpoints writes none of them. That is a
decline and it is not a pass.

`ParkFactory.Sequences` and `ParkFactory.Letters` are the two bounds the queue
refuses at — the numbers are the implementation's, `ErrParkFull` is what the
framework declares. Declaring neither reports `bounds` as *not certified*.
Declaring one wider than 64 does too: the harness will not write a thousand rows
into somebody's queue to reach a bound, so it is certified over a park configured
smaller.

**These harnesses write rows they do not remove.** Every key, projection name and
identity carries a run identity of its own, so two runs against one table never
read each other's rows; the sweep is the application's, here as everywhere else.

## The twenty sections

`binding` · `stream identity` · `expected version` · `dense versions` ·
`global order` · `conservation` · `stream paging` · `global paging` ·
`resumption` · `bounds` · `payload ownership` · `refusal classes` ·
`cancellation` · `lifecycle` · `concurrency` · `transactions` · `durability` ·
`shared backing` · `monotone visibility` · `store failure classification`

The last five are gated on a capability or a hook; the first fifteen run against
every store.

**`transactions` widened after phase 5, and a store certified before it may go
red on the two clauses that arrived.** The first gives one unit of work **two
streams** and asks that an append to each of them is admitted once that unit has
committed: a store that tracks the streams a transaction took in one variable
rather than in a set commits both and frees one, and the other is held by a claim
nothing will ever release — every later writer of it is refused a conflict there
is no longer a competitor for. The second, which runs only for a store supplying
`Sibling`, carries the unit of work through **a second store value over one
backing**: two values over one backing are one store, so both must answer the
same authority for it and a write issued through either must be inside it. A
store that finds its transaction by the value that opened it leaves every write
a second value makes on autocommit — admitted, invisible to the rollback the
caller believes in, and reported by nothing. Neither clause is a new rule;
nothing in the suite exercised either before.

## The suite is falsified against itself

A conformance suite that stopped checking something looks exactly like a
conformance suite that passes. So both suites ship with an inventory of
deliberately broken stores — a store that publishes a shorter page than it says,
one that reuses a position, one that admits every append whatever version it was
decided at, one that hands back pooled buffers, one that reports it refused after
it had already written — and a test asserts that **each defect fails the section
named for it**. Gut a section and that test goes red rather than the suite going
quiet. The checkpoint runner has its own fourteen, one per section but `durability` —
among them one that ignores the fence, one that answers the cursor saved before
the last one, one whose `Load` ignores its `projection` argument, one that saves
outside the caller's transaction, one that forgets outside it, one that creates a
row at advance 1 over a live one, and one that binds a cursor to the name that
saved it. A second test asserts the other direction, which is the one that
matters when a section is gutted rather than a store broken: **every section is
named by a defect that breaks it**, and the one exemption — `durability`, whose
defect is a factory rather than a decorator — is written down so the list can
only shrink.

The three application harnesses ship the same two tests over inventories of
their own — ten ledgers, six ownership rows and eight queues, each one thing
written wrong. Among them: a claim that reads the key before it inserts it and
decides from the read, a ledger that reports every claim took the key, an
ownership row that reads the row and then writes it rather than moving it with
one fenced statement, one that reads on a handle of its own while its write rides
in the caller's unit, one that takes no lock at all, a queue that answers the
blocking test from the snapshot it took the first time it was asked, and one that
counts the letters it was handed rather than the rows its table holds. Each is
asserted to fail the section named for it, with the same implementation minus the
defect as that section's control. Five of them are driven again through live
PostgreSQL in `eventpg`'s own suite, in a subprocess, so what is measured there
is a real index, a real row lock and a real snapshot rather than a model of one.

## The three proxies

`Run` certifies a store. The three proxies certify **your declaration**, and they
need no store at all:

- `Keys` renders every identity you give it and reports the pair that collides —
  two identities rendering one key are two aggregates over one history, folding
  each other's facts, with no error at any point.
- `Families` reports two declarations naming one family, which is the same
  failure one level up.
- `RoundTrip` encodes one sample per retained revision with that revision's own
  codec, reads it back, and reports a codec that drops part of what it was given,
  one that cannot read its own output, and one that hands back memory it will
  reuse. A struct with no exported field, no `Equal` of its own and no `==` —
  a `big.Int`, and every value object neither method could be declared on — is
  settled by re-encoding what came back and comparing the bytes. It also reports
  what it **could not** establish rather than passing: a sample holding more than
  65 536 values is past what the comparison walks, a payload type whose own
  `Equal` panicked was never compared, and a payload holding a **function** is
  recorded by no wire format and compared by nothing. A **marker** fact
  — `struct{}`, whose reader type holds one value — passes on the fidelity half
  alone; there is no second payload for the non-aliasing half to disturb anything
  with. See [event](event.md) for the whole of what a green run means.

Run all three in the package that holds your declaration. They are cheap, they
run under `make unit`, and each of them catches a class of defect whose only
other symptom is a wrong answer months later.

## See also

- [event](event.md) — the vocabulary and the two seams these suites are written for
- [eventmemory](eventmemory.md) — the store that ships, and the first to run it
- [projection](projection.md) — the consumer a certified checkpoint store serves, and
  where `Generations` and `Park` are declared
- [receipt](receipt.md) — where `Ledger` is declared
- [[D-121]] · [[D-126]] · [[D-128]] · [[D-133]] · [[D-141]] · [[D-142]] ·
  [[FL-036]] · [[FL-038]] · [[FL-042]] · [[FL-043]] · [[UC-032]]
