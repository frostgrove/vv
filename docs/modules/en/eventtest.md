# event/eventtest — the store contract, as a suite your store runs

```go
import "github.com/frostgrove/vv/event/eventtest"
```

**Module:** in the root module — standard library, `testing` and `event` only
· **Depends on:** [event](event.md) · **Depended on by:** nothing

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
| `RunCheckpoints(t, factory)` | the twelve-section suite for an `event.Checkpoints`, one `t.Run` per section |
| `CheckpointFactory` | `New`, `Begin`, `Sibling`, `Cursor`, `Instant`, `Window` |
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

## The twelve checkpoint sections

A checkpoint store is the second seam this package certifies, and it is certified
the same way — the same three words, the same anti-vacuity rules, the same
"a hook that is missing is not a capability that is not claimed".

`binding` · `absence` · `round trip` · `fence` · `forget` · `names` · `bounds` ·
`refusal classes` · `lifecycle` · `concurrency` · `transactions` · `durability`

`transactions` runs only for a store claiming `Transactions` and `durability`
only for one claiming `Persistence`; a store claiming either and supplying no
hook fails the run before a section starts. So `eventmemory.Checkpoints` is
certified on eleven and reports `durability: not certified`, and live
`eventpg.Checkpoints` passes twelve.

| Hook | Required when |
|---|---|
| `New` | always. It is called several times per section and a later value must not reset what an earlier one wrote — a section reads back through a second value what a first one wrote |
| `Begin` | the store claims `Transactions` |
| `Sibling` | the store claims `Persistence` — a second value over the same backing, which is what a restart is |
| `Cursor` | always. A cursor **of the log this store records against**, real rather than a literal the suite invented, and two consecutive calls must answer two different cursors: a section that cannot tell a stale answer from a fresh one certifies a store that never wrote |
| `Instant` | never; it is the grain the store's own instant column keeps, and without it the suite measures at its own |

Two of the twelve are worth naming here because they are obligations a
third-party store might not expect. **`round trip` requires the cursor that went
in to be the one that comes back, byte for byte, through a second value** — a
store that re-encodes it is refused there, and a projection's settlement compares
cursors to tell its own save from a second instance's. And **`bounds` requires
the empty cursor to be refused and no row to move**: the empty cursor is the
origin of a log, so a row carrying one at a live advance restarts a consumer at
the beginning against a live destination.

```go
func TestMyCheckpointsSatisfyTheContract(t *testing.T) {
	eventtest.RunCheckpoints(t, eventtest.CheckpointFactory{
		New:    func(t *testing.T) event.Checkpoints { return open(t) },
		Cursor: func(t *testing.T) event.Cursor { return mint(t) },
	})
}
```

## The twenty sections

`binding` · `stream identity` · `expected version` · `dense versions` ·
`global order` · `conservation` · `stream paging` · `global paging` ·
`resumption` · `bounds` · `payload ownership` · `refusal classes` ·
`cancellation` · `lifecycle` · `concurrency` · `transactions` · `durability` ·
`shared backing` · `monotone visibility` · `store failure classification`

The last five are gated on a capability or a hook; the first fifteen run against
every store.

## The suite is falsified against itself

A conformance suite that stopped checking something looks exactly like a
conformance suite that passes. So both suites ship with an inventory of
deliberately broken stores — a store that publishes a shorter page than it says,
one that reuses a position, one that admits every append whatever version it was
decided at, one that hands back pooled buffers, one that reports it refused after
it had already written — and a test asserts that **each defect fails the section
named for it**. Gut a section and that test goes red rather than the suite going
quiet. The checkpoint runner has its own five: one that ignores the fence, one
that answers the cursor saved before the last one, one that reports absence for a
row that exists, one whose `Load` ignores its `projection` argument, and one that
saves outside the caller's transaction.

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
- [projection](projection.md) — the consumer a certified checkpoint store serves
- [[D-121]] · [[D-128]] · [[D-133]] · [[FL-036]] · [[FL-038]] · [[UC-032]]
