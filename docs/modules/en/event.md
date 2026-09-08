# event — an append-only fact history, over any store

```go
import "github.com/frostgrove/vv/event"              // the vocabulary and the store seam
import "github.com/frostgrove/vv/event/eventmemory"  // a complete in-memory store
import "github.com/frostgrove/vv/event/eventtest"    // the conformance suite
```

**Module:** all three are in the root module. A module boundary here is a
third-party dependency boundary, and none of them costs one ([[D-116]],
[[D-121]]) · **Depends on:** `crud` for the classes a transport already maps and
the data-source comparison a backing is, `errs` for the fault an over-a-bound
refusal carries · **Depended on by:** nothing, on purpose — a graph test refuses
the first base package that imports it

You declare an aggregate, its facts and how each one folds. The framework
records the facts, replays them into a state, and admits a write only at the
version the decision was made at. It never opens a transaction, never retries,
and never rewrites a fact.

---

## What you get

### The declaration

| | |
|---|---|
| `Define[S, ID](family, key)` | an `*Aggregate[S, ID]`: a family name and the mapper from your identity to a stream key. Panics on a malformed declaration |
| `TryDefine[S, ID](family, key)` | the same, returning the error instead ([[D-123]]) |
| `Declare[S, ID, E](a, name, chain, fold)` | a `*Fact[S, ID, E]`: a wire type name, its reader chain and the fold that applies it. Panics |
| `TryDeclare[S, ID, E](a, name, chain, fold)` | the same, returning the error |
| `From[V](codec)` | a `Chain[V]` at revision 1 |
| `Then[A, B](prev, codec, up)` | the next revision, with the upcaster that carries the old shape forward |
| `JSON[V]()` | the shipped `Codec[V]`, charged when it is built ([[D-124]]) |
| `Codec[V]` | `Encode` · `Decode` · `CanEncode` — the second extension point |
| `Compose(parts…)` | a composite identity rendered into one `Key`, byte for byte forever ([[D-125]]) |
| `Aggregate.Family()` · `Aggregate.Key(id)` · `Aggregate.Fold(id, state, changes…)` | the declaration's own readers; the fold needs no store |
| `Fact.Name()` · `Fact.Revisions()` · `Fact.New(id, payload)` · `Fact.RoundTrip(byRevision…)` | the fact's identifiers, the decision, and the proxy that proves a payload survives its own codec |
| `Change[S]` | one decision: `Stream()` and `Err()` |
| `Declaration` | the one non-generic view of an aggregate |

```go
type Ledger struct{ Balance int64 }

type AccountID struct{ Tenant, Number string }

type Opened struct{ Owner string }
type Credited struct{ Amount int64 }

var Account = event.Define[Ledger, AccountID]("account", func(id AccountID) event.Key {
	return event.Compose(id.Tenant, id.Number)
})

var (
	Open   = event.Declare(Account, "account.opened", event.From(event.JSON[Opened]()), openLedger)
	Credit = event.Declare(Account, "account.credited", event.From(event.JSON[Credited]()), creditLedger)
)

func openLedger(state Ledger, fact Opened) Ledger { return state }

func creditLedger(state Ledger, fact Credited) Ledger {
	state.Balance += fact.Amount
	return state
}
```

A revision is the **position** in the chain, never a number you type:

```go
var Note = event.Declare(Account, "account.noted",
	event.Then(event.From(event.JSON[NoteV1]()), event.JSON[NoteV2](), upgradeNote), noteLedger)
```

### What `JSON[V]()` refuses, and when

`event.JSON[V]()` asks once, when the codec value is built, whether a `V` written
by `encoding/json` reads back as itself — so a payload shape that would encode as
`{}` and replay as a zero value is a panic at `Declare`, not a fact log you
cannot repair ([[D-124]]). The one that surprises people is **embedding**:

```go
type Money struct{ Cents int64 }              // with MarshalJSON/UnmarshalJSON

type Line struct {
	Money                                     // refused: Go promotes Money's pair
	SKU   string                              // onto Line, so SKU is written by nobody
}

type Wrapped struct{ Line }                   // refused for the same reason, one hop out

type Priced struct {
	Amount Money                              // accepted: a named field is not a promotion
	SKU    string
}
```

Promotion is a language rule: no tag on the embedded field undoes it, `json:"-"`
included, and it carries through as many embeddings as it takes — so the question
is asked along the whole chain and the refusal names the hop that hides the
field. The remedies the refusal itself names are to name the embedded field, or
to declare a codec of your own.

### The composition root

| | |
|---|---|
| `Open(store)` | a `*Binding` — the table of families bound through it. There is no package-level registry |
| `Bind[S, ID](b, a)` | a `*Repo[S, ID]`: five constant-time checks and one allocation, so a store per request is ordinary |
| `Read(log, after)` | a `*Reader` over the whole log, from a cursor |
| `ReadOnly(store)` | the store as a `Log`, with no way back to the append surface |

### The write path

| | |
|---|---|
| `Repo.Load(ctx, id)` | the state folded from the whole history, and the `At[S]` token it was at |
| `Repo.Append(ctx, at, changes…)` | admitted only at the token's version; answers the next token and a `Commit` |
| `Repo.Within(ctx)` | the context marked with the transaction this store finds in it |
| `Repo.Authority(ctx)` | what this store says is bound, for two subsystems proving they wrote together |
| `At[S]` | `Stream()` · `Version()` — minted by `Load` and by a successful `Append` |
| `Commit` | `Empty()` · `Stream()` · `First()` · `Last()` · `Count()` · `Authority()` |

```go
state, at, err := repo.Load(ctx, id)
if err != nil {
	return err
}
if state.Balance < amount {
	return ErrInsufficient
}
_, receipt, err := repo.Append(ctx, at, Credit.New(id, Credited{Amount: -amount}))
```

### The log walk

| | |
|---|---|
| `Reader.Next(ctx)` | one page; `false` when the page was empty |
| `Reader.Events()` | what the last `Next` fetched — yours, including its capacity |
| `Reader.Cursor()` | safe to persist and to resume from in another process — unless the walk ran on a context carrying a transaction of this backing |

### The store seam

| | |
|---|---|
| `Log` | `Capabilities()` · `Limits()` · `Backing()` · `ReadAll(ctx, after)` |
| `Store` | `Log`, plus `Transaction(ctx)` · `ReadStream(ctx, s, after)` · `Append(ctx, req)` · `Close()` |
| `Capabilities` | `Transactions` · `Persistence` · `MonotoneVisibility` · `SharedBacking`, each a `Support` |
| `Support` | `Unstated` · `Unsupported` · `Supported` — three states, because a capability nobody stated is not one nobody has |
| `Limits` | `MaxPayload` · `MaxBatch` · `MaxKey` · `StreamPage` · `MaxRead` |
| `Record` / `AppendRequest` / `Envelope` | what crosses the seam: bytes, a stream, a version, a position, an instant. Never a Go type |
| `Backing` / `NewBacking(identity)` | what a store writes to; compared with `Equal`, never with `==` |
| `Authority` / `NewAuthority(over, tx)` | a transaction's identity; compared with `Same` |
| `Outcome` | `Unclassified` · `Conflict` · `NotWritten` · `Unconfirmed` · `Closed` · `BadCursor` · `Refused` |
| `Failure(outcome, cause)` | the whole of the store-to-kernel error channel ([[D-122]]) |
| `CauseOf(err)` | the one reader that reaches a store's own cause |

All eight methods are required, so the value your composition root handed over is
the value that answers every question — a decorator cannot be walked past
([[D-061]]).

Two answers are the **store's** and not the framework's, and a consumer that
depends on either has written to one store rather than to the seam:

- `ReadStream` inside a bound transaction returns that transaction's own staged
  appends. `ReadAll` inside one is unspecified — a store reading through the
  transaction it joined returns its uncommitted events, a store whose global
  order is assigned at commit returns none, and both are conformant. No path the
  framework initiates opens a transaction and then walks the log — but that is
  not a promise that no walk runs inside one: `Reader.Next` issues the read on
  whatever context you hand it, and neither it nor `Read` refuses one carrying a
  transaction. The obligation is yours, and it is that a cursor from such a walk
  must not be persisted: a rollback then discards events it is already past.
- `Envelope.Position` on an envelope that has not been committed is unspecified
  for the same reason — zero from one store, a real sequence value from another.
  `Envelope.Version` is not: it is the version the append was admitted at, before
  the commit and after it.

### The ceilings

| | |
|---|---|
| `MaxPayloadBytes` · `MaxNameBytes` · `MaxKeyBytes` | `1 MiB` · `128` · `2 KiB` |
| `MaxBatchCount` · `MaxPageCount` | `1024` · `4096` |
| `MaxResidentBytes` · `ResidentPage(maxPayload)` | `64 MiB`, and the page count it becomes at a payload bound |

A store publishes its own numbers in `Limits` and the framework refuses any of
them above the ceiling for it, at both doors — `Bind` and `Read`. A store that
states a zero is refused rather than defaulted: `Limits` is what the framework
enforces on the store's behalf, so a zero there is a store that forgot to state
a bound.

## The refusals, and the six classes

`errors.Is` never crosses a class, so a caller who branches on one is never
answered by another.

| Class | Sentinels |
|---|---|
| declaration — you wrote the declaration wrong; panicked, never returned | `ErrDeclaration` · `ErrSealed` · `ErrCodecType` |
| wiring — this program was assembled from values that do not belong together | `ErrFamily` · `ErrWrongStore` · `ErrWrongStream` · `ErrNoTransaction` · `ErrNoTransactionBinding` · `ErrAmbientNotTransaction` · `ErrTransactionMismatch` · `ErrCursor` |
| request — the data this operation was given cannot be used | `ErrKey` · `ErrEncode` · `ErrSample` · `ErrTooLarge` |
| history — a fact's recorded bytes cannot be read by this build | `ErrUnknownType` · `ErrRevision` · `ErrPayload` · `ErrUpcast` |
| write — the append did not do what you asked | `ErrConflict` · `ErrUncertain` |
| store — the store itself refused or failed | `ErrBackend` · `ErrClosed` · `ErrRefused` |

The request class wraps `crud.ErrBadRequest`, `ErrConflict` wraps
`crud.ErrConflict`, and a retryable backend failure wraps `crud.ErrUnavailable`,
so a transport answers the status the class implies without a table of its own.
`ErrTooLarge` carries an `*errs.Fault`.

A refusal renders its own sentinel and nothing of what produced it — no key, no
payload, no version, no position, no cursor, and no driver's or codec's text.
The cause is reachable, deliberately and by name, through `CauseOf`.

## A conflict is not retried, by anybody

`ErrConflict` says the stream is not at the version the decision was made at, and
it deliberately does not say what version it *is* at. Reporting that invites
re-appending the same facts at a fresh version, which writes a stale decision.
Reload, decide again, append again — and where a transaction is open, the retry
unit is the **transaction**, because a losing insert poisons the block on a SQL
store.

`ErrUncertain` is a different answer: the append was issued and nobody confirmed
it. Reading `err != nil` as "nothing was written" is exactly the inference it
exists to refuse.

## The transaction is yours

`Within` asks the store what this context carries and marks the context with the
answer; it opens nothing. An append made while a transaction of the store's own
backing is bound is written inside it and disappears with a rollback. The
framework issues no begin, no commit and no rollback anywhere ([[D-118]] is the
same rule for durable work), and an AST check over the package is what holds it.

Two markers compose: `Within` over store A and then over store B leaves both,
resolved by backing, so an ordinary program with two stores is not a trap.

## A declaration seals itself

The fact table is writable until the first party observes it and read-only for
the whole of its observable life afterwards — which is what makes one
`*Aggregate` shared by every request goroutine safe without a lock on the read
path. A fact declared after that is `ErrSealed`. The six readers that seal are
enumerated in the source, and a seventh without a row there fails a test.

## A declared identifier: the family and the wire type name

The two names `MaxNameBytes` governs are held to one rule: non-empty, valid
UTF-8, no control character, within the cap — and no `[` or `]`, because a
refusal renders a stream as one bracketed field and either name reaching that
line carrying a bracket closes the field early and writes a second one after it.
Quoting does not answer it: Go's quoting leaves both characters alone.

Each name crosses two doors and the rule is at both. At the declaration,
`Define` and `Declare` refuse it with `ErrDeclaration`, so a name a program
declared always reads as itself in a log line. At an **envelope**, the name was
declared by nobody this program can see: a family that does not pass renders as
`[stream unnameable]`, and a recorded type name that does not pass names no
declared fact and can name none, so it is `ErrUnknownType` and does not travel
into the message.

## One family names one aggregate

Two aggregates over one family share a history — both fold each other's facts,
both append at versions derived from the other's. `Bind` refuses the second
declaration of one family through one `*Binding` with `ErrFamily`. Two
`*Binding` values cannot see each other, so a `Change` also names the aggregate
it was decided on: folding or appending one through another aggregate of the
same family is that same `ErrFamily`, and `eventtest.Families` is the runnable
proxy over every declaration one application composes.

## Ownership: everything handed over is the receiver's

A payload you hand to `Fact.New` is cloned at the moment of decision, so mutating
it afterwards cannot change what was recorded. A page a store hands back belongs
to whoever received it, indefinitely, including its capacity. A store may read
the framework's slices for the duration of a call and must never write into them
or retain them.

`Fact.RoundTrip` is the runnable proxy for the codec half: it encodes each
retained revision's sample with that revision's own codec, reads it back, and
refuses a codec that hands back memory it will reuse.

**What a green round trip means, and what it does not.** It proves two things
and each has a stated edge:

- **Fidelity** — what came back is what was given, compared in the revision's
  own type over the **exported** half of a struct. An unexported field is your
  own business and is not compared; a struct whose fields are *all* unexported
  is compared by its own `Equal(T) bool` if it declares one — which is how a
  `time.Time` answers at all, and the only answer that can be right for it —
  and by `==` if it does not, which is how a `netip.Addr` answers. A struct
  with neither, such as one holding a private slice or a `big.Int`, is settled
  at the **wire**: what came back is encoded again and the bytes are compared
  with the ones the sample encoded to, so a codec that drops the amount and
  keeps the note beside it is `ErrPayload` like any other difference. That
  question needs no method, which is why it is the one asked of a type you do
  not own — no `Equal` can be declared on a `math/big` value from your package.
  It is the weaker of the two and runs only where the value comparison had
  nothing to say: a codec that drops the same thing on the way out and on the
  way in re-encodes to the bytes it was given, and only comparing the values
  finds that. `Equal` is asked at that position and nowhere else, so an `Equal`
  written for your own question — one that compares a title and not a body —
  cannot switch this check off for a struct the field walk could answer; and
  nothing is asked beside it, because a codec that hands a `time.Time` back in
  UTC is faithful by that type's own answer and writes other bytes.
- A **function** is the one leaf both answers are silent for: no wire format
  records one and Go compares no two, so a payload holding one is refused with
  `ErrSample` naming it. Record what identifies the behaviour — a name, a code
  — and choose the function from that when you fold. A channel is comparable,
  so it is compared, and a codec that hands back another one is a difference.
- Behind an **interface** the two values may have different types, which is a
  codec substituting one value for another. That is a difference like any
  other (`ErrPayload`), never a walk of one by the other's shape. Two numbers
  holding one value are the exception, because no wire format this admits has
  an integer distinct from a float: an `int64` read back as a `float64` of the
  same value passes, and as a different one does not. The exception stops
  where the float does. An integer a `float64` cannot hold exactly — an id or
  an amount above 2^53, which is the ordinary size of a snowflake or a satoshi
  balance — read back as the nearest float is a difference like any other, and
  is `ErrPayload`.
- **Non-aliasing** — the same bytes are decoded twice and the two answers are
  searched for a slice or a map they share, and the codec is made to decode a
  second payload between two encodings of the first answer.

Two facts the second half cannot be run for: a **marker** fact, whose reader
type holds one value — `struct{}` — because its only sample encodes as its own
zero value and there is no second payload to disturb anything with, and a fact
whose sample you gave as the zero value of a type that carries data, which is
refused as a caller mistake (`ErrSample`) rather than reported as a pass. A
marker fact passes: fidelity is proved, non-aliasing is not under test, and a
type of size zero holds one value however it is spelled, so one that forbids
`==` is not asked for one.

Both walks visit at most **65 536 values**. A sample larger than that is refused
with `ErrSample` naming the bound, because a walk that stopped early compared
nothing past where it stopped — round-trip a smaller sample of the same type. A
payload type's own `Equal` is called only at the position above, and its panic is
`ErrSample` too.

## Testing without a store, and testing a store

`Aggregate.Fold` runs your folds over your value with no store at all, so a
domain rule is an ordinary pure function under test. `eventmemory` is a complete
store for everything above that, and `eventtest` is the contract as a suite a
store implementer runs against their own store.

Three proxies run over your own declaration rather than over the framework's:
`eventtest.Keys` for the mapper's injectivity, `eventtest.Families` for one
family per aggregate, and `eventtest.RoundTrip` for a payload that survives its
own codec.

## What this is not

There is no query language over history, no list-filter-sort surface, no
delete and no update: the two reads are one stream in version order and the log
in commit order. There is no snapshot, no projector, no subscription and no
publisher — a walk over the log is what those are built from, and what is built
on it is at least once.

## See also

- [eventmemory](eventmemory.md) — the store that ships
- [eventtest](eventtest.md) — the conformance suite, and the three proxies
- [[D-121]] · [[D-122]] · [[D-123]] · [[D-124]] · [[D-125]] · [[FL-036]] ·
  [[UC-032]]
