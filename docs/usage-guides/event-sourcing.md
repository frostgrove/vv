# Record what happened, and read it back

The module references describe the API — [event](../modules/en/event.md),
[projection](../modules/en/projection.md), [receipt](../modules/en/receipt.md),
[eventpg](../modules/en/eventpg.md). This describes the **adoption**: what you
write, in what order, and what you have to decide along the way.

Everything below runs against the repository's own dev database:

```bash
make up     # from the repository root
```

Every Go block on this page is compiled. `_examples/event-guide` is this page's
code and nothing else, and `TestEveryGoFenceInTheEventSourcingGuideIsCompiled`
compares the two — so a call that does not type-check is a red test rather than
a consumer's afternoon.

---

## Part I — what you get (read this first)

Your domain is a struct, a set of facts and one fold per fact. Deciding is a
pure function, and nothing in it knows about a database:

```go
state, at, err := orders.Load(ctx, id)          // folded from the whole history
if err != nil {
	return err
}
if state.Status != Draft {
	return ErrNotDraft
}
_, commit, err := orders.Append(ctx, at, Placed.New(id, OrderPlaced{Total: total}))
```

`Append` is admitted **only** at the version `Load` handed you. Two writers who
loaded the same state cannot both win: exactly one is admitted and the other is
told the stream moved, in a class of its own.

A read model follows the log on its own supervised runner:

```go
supervisor, err := runtime.Auto(following)       // projection.New(...) built below
```

After a confirmed command, the next read does not show the old state — and your
test does not sleep:

```go
mark, err := waiting.Committed(ctx, store, commit)   // after the transaction committed
waiting.Until = mark
_, err = projection.Wait(ctx, waiting)
```

And when the connection dies before the acknowledgement arrives, the caller can
ask what happened, by key, from another process:

```go
resolution, err := receipt.Resolve(ctx, receipt.ResolveSpec{
	Ledger: ledger, Store: store, Key: key,
})
switch resolution.Standing {
case receipt.Found:       // here is the range it wrote
case receipt.Unresolved:  // still in flight — do NOT re-issue it
}
```

Four things you never write: a migration for the event tables (there is a schema
step), a retry (the unit is yours), a `sleep` (there is a wait), and an
`ORDER BY` over history (there is no query language over events).

---

## Part II — declare the aggregate

One file, in your own package. Nothing is registered by importing it.

```go
type Order struct {
	Status Status
	Total  int64
}

type OrderID struct{ Tenant, Number string }

type OrderPlaced struct{ Total int64 }
type OrderShipped struct{ Carrier string }

var Orders = event.Define[Order, OrderID]("order", func(id OrderID) event.Key {
	return event.Compose(id.Tenant, id.Number)
})

var (
	Placed  = event.Declare(Orders, "order.placed", event.From(event.JSON[OrderPlaced]()), place)
	Shipped = event.Declare(Orders, "order.shipped", event.From(event.JSON[OrderShipped]()), ship)
)

func place(state Order, fact OrderPlaced) Order {
	state.Status, state.Total = Placed_, fact.Total
	return state
}

func ship(state Order, fact OrderShipped) Order {
	state.Status = Shipped_
	return state
}
```

**Four decisions you are making here.**

1. **The family name is permanent.** It is half of every stream key ever written.
   One family names one aggregate; two declarations of one family share a history
   and are refused wherever the framework can see both.
2. **The identity mapper must be injective.** `event.Compose` escapes its parts,
   so `Compose("a", "b:c")` and `Compose("a:b", "c")` are different keys. Prove it
   over your own identities with `eventtest.Keys`, which is a test you write once.
3. **The wire name is permanent too.** `"order.placed"` is what is stored in every
   row; the Go type name is not.
4. **A payload shape may change, and each shape is a retained revision.** Add the
   new shape with `Then` and write the conversion from the old one; the revision
   number is the position in that chain and is never a number you type.

Then check your own payloads, without a database:

```go
func TestPayloadsRoundTrip(t *testing.T) {
	eventtest.RoundTrip(t, Placed, OrderPlaced{Total: 1200})
	eventtest.Keys(t, Orders, OrderID{"acme", "1"}, OrderID{"acme", "2"})
	eventtest.Families(t, Orders)
}
```

That catches the three silent ones: a reader that drops part of what it was
given, one that cannot read back its own output, and one that hands back memory
it will overwrite.

---

## Part III — bind a store

The declaration is a value; a repository is the declaration plus a store. The
store needs **both** halves of one connection — the `*sql.DB` it reads the schema
through and the `crud.Source` it sees your transaction through — and it refuses a
spec carrying one without the other, and a pair that names two different
databases, at construction:

```go
db, err := sql.Open("pgx", dsn)
if err != nil {
	return err
}
source := crudsql.Postgres(db)

store, err := eventpg.New(eventpg.Spec{DB: db, Source: source, Schema: eventpg.Schema{Name: "events"}})
if err != nil {
	return err
}
orders, err := event.Bind(event.Open(store), Orders)
```

`event.Open` is the binding table, and it is the first argument because that is
what a repository is bound *through*: it holds the families bound through this
one value, so two of them cannot see each other and no import registers
anything.

**The schema is a deployment-profile choice, not a start-up side effect.** A
development profile runs `Prepare`, which creates and upgrades; a production one
runs `Check` or `VerifySchema`, which creates nothing and refuses a schema it does
not recognise. Choose it where you choose every other profile setting.

**The transaction is yours, from the first statement to the last.** This
framework opens none, commits none and rolls back none:

```go
err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
	state, at, err := orders.Load(ctx, id)
	if err != nil {
		return err
	}
	_, commit, err = orders.Append(ctx, at, decide(state, command)...)
	return err
})
```

A conflict is **not** retried by anybody. Reload, decide again, append again —
and where a transaction is open, the unit of the retry is the **transaction**,
because a losing insert poisons the block on a SQL store.

For tests with no database at all, `eventmemory.New` is a complete store with
transactions. It is not a test double; what it declines is persistence, and it
says so.

---

## Part IV — follow the log into a read model

```go
router := projection.NewRouter(projection.SkipForeign)
projection.On(router, Placed, readModel.placed)
projection.On(router, Shipped, readModel.shipped)

spec := projection.Spec{
	Name:        "orders",
	Log:         event.ReadOnly(store),
	Checkpoints: checkpoints,
	Handler:     router,
	Advance:     projection.InUnit,
	Unit: func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, work)
	},
	Destination: source,
}

following, err := projection.New(spec)
if err != nil {
	return err
}

supervisor, err := runtime.Auto(following)   // or runtime.NewSupervisor(runtime.Spec{Runners: ...})
if err != nil {
	return err
}
return supervisor.Start(ctx)
```

A supervisor takes its runners at construction and has no `Add`: what starts is
the set the composition root named, which is what makes "every runner this
process runs" a value a reviewer can read in one place.

**What you are deciding here.**

- **`Advance: InUnit`** puts the checkpoint advance in the same transaction as
  your handler's writes, so a crash takes both back. It needs `Unit` **and**
  `Destination`, and the framework checks that the handler writes where the
  advance does. `AfterApply` is the honest alternative when the two cannot be one
  transaction, and it promises what its name says.
- **`SkipForeign` versus `RefuseForeign`.** A read model over one family skips
  the rest; a router that claims to cover everything refuses what it does not
  route rather than passing it.
- **Delivery is at least once, in both modes.** Make the handler idempotent —
  upserting on `(stream, version)`, which every envelope already carries, is the
  whole of it.
- **There is no head, and no "caught up".** `PhaseFollowing` means *the last read
  delivered nothing*.

Add a park when a permanently failing envelope must not block the whole read
model, and remember what it costs you to leave it out:

```go
spec.Park = parkTable                      // your own table, behind the interface
spec.OnPermanentFailure = projection.ParkSequence
```

With a park, a permanent failure parks the failing envelope **and the following
envelopes of its sequence**, in the same commit as the read model and the
advance, and an operator drains it with a `Redrive`. Without one, a permanent
failure **halts** the projection, which is loud and is sometimes what you want.

---

## Part V — read your own change back, without sleeping

Build the wait **once, at the composition root**, from the same `Spec` the runner
was built from:

```go
whole, err := projection.NewCover(projection.Whole())
if err != nil {
	return err
}
waiting, err := projection.WaitOf(spec, whole)
```

`WaitOf` is the spelling to use. Three of the five facts a wait needs are
silently wrong-able by a request handler that does not read the projector's
wiring, and deriving them is what stops that.

A `Cover` is not a `Partition`. `projection.Whole()` is one member — the whole
key space, which is what a projection that was never partitioned already is —
and `NewCover` is the value that checks the set they form, which is why a wait
takes the second and never the first.

Then, in the handler, **after** the transaction has committed:

```go
mark, err := waiting.Committed(ctx, store, commit)
if err != nil {
	return err
}
waiting.Until = mark

ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
defer cancel()

vis, err := projection.Wait(ctx, waiting)
switch {
case err == nil:
	return serveFresh()
case errors.Is(err, projection.ErrParked):
	return serveParked()                    // a redrive is the fix; waiting is not
case errors.Is(err, projection.ErrNotVisible):
	return serveStale(vis.Behind)           // your branch, not a flag
default:
	return err
}
```

**Three things that go wrong here, and each is silent.**

1. **Minting the mark inside the writing transaction.** It is refused, and the
   reason is worth knowing: a position read inside the transaction that wrote it
   belongs to an append that can still roll back, and a rolled-back append's
   position is never delivered to anybody — so a wait on such a mark would never
   reach.
2. **Waiting on a mark another projection minted.** Two projections over one log
   is ordinary, the values are each individually right, and the result would be
   `Reached` for an event the second projection parked. It is refused at the
   door. To wait on the second one, derive **its** `WaitSpec` and call **its**
   `Committed`.
3. **Waiting without the park.** Then `Reached` means *delivered*, not *applied*.
   `WaitOf` supplies it from the spec, which is the reason to use `WaitOf`.

**What it costs.** One park count plus one checkpoint load per cover member, per
poll, per waiting caller, at `Every` (50 ms by default) — and **two** loads per
member while that member has recorded no row yet. So `1 + |cover|` reads a poll
healthy and `1 + 2 x |cover|` during a rebuild: four members at the default is
**100** small indexed `SELECT`s a second per waiter, **180** while that generation
is still rebuilding, and one more per poll while its park queue is not empty. The
lever is `Every`, and a request path that wants 200 ms writes 200 ms. The full
derivation, including the two `Generations.Active` reads a whole wait pays, is on
[projection](../modules/en/projection.md).

---

## Part VI — answer "did my command happen?"

This part is optional and costs one table **you** own, in the same database as
the events.

```sql
CREATE TABLE receipts (
	key            text PRIMARY KEY,
	fingerprint    text NOT NULL,
	family         text NOT NULL,
	stream_key     text NOT NULL,
	first_version  bigint NOT NULL DEFAULT 0,
	last_version   bigint NOT NULL DEFAULT 0,
	complete       boolean NOT NULL DEFAULT false,
	recorded_at    timestamptz NOT NULL
);
```

Implement `receipt.Ledger` over it. **The claim is two statements and their order
is the mechanism** — `INSERT … ON CONFLICT (key) DO NOTHING` and then a `SELECT`
of the same key, in the caller's one transaction. `_examples/event-receipts` is
the reference implementation and a test compares its statements with the ones the
live suite ran, byte for byte.

Then the command becomes:

```go
key, err := receipt.NewKey(request.Header.Get("Idempotency-Key"))
if err != nil {
	return err
}

err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
	state, at, err := orders.Load(ctx, id)
	if err != nil {
		return err
	}
	changes := decide(state, command)

	print, err := orders.Digest(at, changes...)
	if err != nil {
		return err
	}
	fingerprint, err := receipt.NewFingerprint(print)
	if err != nil {
		return err
	}

	held, err := receipt.Once(ctx, receipt.ClaimSpec{
		Ledger: ledger, Store: store, Key: key,
		Fingerprint: fingerprint, Stream: at.Stream(),
	}, func(ctx context.Context) (event.Commit, error) {
		_, commit, err := orders.Append(ctx, at, changes...)
		return commit, err
	})
	if err != nil {
		return err // a collision, or a row nobody resolved
	}
	answer = held.Receipt() // the range this attempt wrote, or the first one's
	return nil
})
```

**What you are signing up for.**

- **The claim comes before the decision.** Recording after the append is safe
  against two writers at one version and **not** against two retries at two
  versions, which is what a caller that does not know the outcome does.
- **Your payloads must encode to the same bytes twice.** A codec that records a
  clock, a fresh identifier or a map in iteration order makes every retry a
  refusal. That is loud and never wrong — but it is your obligation and nothing
  here can check it.
- **One key covers one append, to one stream.** A command that writes to two
  aggregates needs two keys; derive the second from the first with a suffix.
- **Retention is yours, and it must exceed the window in which a client may
  present the same key again** — conventionally 24 hours for an HTTP idempotency
  key. Nothing here prunes.

---

## Part VII — read history at a version

```go
before, err := orders.StateAt(ctx, id, disputed-1)
```

It folds the complete prefix and **returns no token**, so it cannot be the first
half of a `Load → Decide → Append`. Correcting the past is a new domain command
against the present.

Version zero, a version past the end and a stream with no events are one refusal.
There is **no timestamp boundary** and there is not going to be one by accident:
a recorded instant is a database clock and not an ordering.

---

## Part VIII — the operational edges, before you meet them

The procedures — restore, rollback, redrive, capacity and the four incidents —
are on their own page: [event-operations.md](event-operations.md). These are the
edges you decide about before you ever run one.

- **Set `idle_in_transaction_session_timeout`** on the application role. The
  PostgreSQL log walk waits on the cluster's oldest running transaction id, so one
  leaked connection anywhere stalls every projection over the schema — and, if you
  use receipts, keeps every resolve for that operation `Unresolved` for exactly as
  long. It is the same session, and it is the one setting that matters.
- **Partition when the handler is the bottleneck**, not when the read is: N
  partitions are N independent walks of the log and N times the read traffic. The
  useful range is 8–16. Only the first start chooses the topology; the route from
  a projection that has run is a `Split`.
- **Rebuild beside the live one.** A generation is a number in the recorded name,
  so `orders@2` runs beside `orders`, is measured against a barrier `Cutover`
  derives from the retiring generation's own rows, and is switched in by one
  fenced write. Add `Spec.Generations` to the **live** projection one release
  before the first cutover.
- **Side effects are a capability, not a mode.** A rebuild is a spec with
  `Effects` nil, and `Stage` runs inside the transaction that commits the advance
  — so what it does must be something a rollback can take back. Send the mail from
  a `jobs` worker that drains the staged row.
- **There is no snapshot**, and the trigger for reconsidering that is measured and
  recorded: a deployment measures its own p99 aggregate replay above ~50 ms, which
  is somewhere above 45 000 events at the rates this repository measures.

---

## Where to go next

| You want | Read |
|---|---|
| to run it: restore, rollback, the DLQ, capacity, incidents | [event-operations.md](event-operations.md) — the operator's page |
| the whole API, one page per package | [event](../modules/en/event.md) · [projection](../modules/en/projection.md) · [receipt](../modules/en/receipt.md) · [eventmemory](../modules/en/eventmemory.md) · [eventpg](../modules/en/eventpg.md) · [eventtest](../modules/en/eventtest.md) |
| to write your own store or checkpoint store | [eventtest](../modules/en/eventtest.md) — the contract as a suite you run |
| a runnable program | [`_examples/event-wait`](../../_examples/event-wait/) · [`_examples/event-receipts`](../../_examples/event-receipts/) · [`_examples/event-partitions`](../../_examples/event-partitions/) · [`_examples/event-generations`](../../_examples/event-generations/) · [`_examples/event-checkpoints-elsewhere`](../../_examples/event-checkpoints-elsewhere/) |
| why any of it is shaped this way | [[D-121]] · [[D-126]] · [[D-128]] · [[D-129]] · [[D-130]] · [[D-132]] · [[D-140]] · [[D-141]] · [[D-142]] · [[D-143]] · [[D-144]] · [[D-145]] |
| where it happens, file by file | [[FL-036]] · [[FL-037]] · [[FL-038]] · [[FL-042]] · [[FL-043]] |
| what a consumer is promised | [[UC-032]] · [[UC-036]] · [[UC-037]] · [[UC-038]] |
