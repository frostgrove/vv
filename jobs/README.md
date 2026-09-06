# Jobs

Frostgrove owns delivery, leases, retries, recovery, fencing and worker lifecycle. The concrete
application owns every durable job name and payload version.

The default Fx path keeps the declaration, handler and wire contract together and builds the
catalog and registrations from one immutable list:

```go
var ReindexJob = jobsfx.AutoFor[*ReindexHandler, ReindexPayload](jobs.Heavy).
	JSON("search.reindex", 1)

var appJobs = jobsfx.MustRegistry(
	ReindexJob,
)

func Jobs() jobsfx.Option {
	return appJobs.Module()
}
```

There is no jobs code generator or jobs manifest. Renaming a Go type or function does not change
the durable identity because the application keeps `"search.reindex"` and version `1` explicitly.
Registry construction rejects unresolved declarations, duplicates and invalid catalogs before
workers start.

Use `TrustedJSON(name, version)` only when the application intentionally allows payload JSON
hooks or interface-driven encoding; `JSON` is the safe default.

`jobsfx.AsConsumer` contributes only a worker consumer. Applications that wire Fx manually must
also provide `jobsfx.AsDeclaration`, set `jobsfx.Spec.Catalog`, or use `jobsfx.Registry`. Keeping
catalog construction independent of handler construction prevents Fx cycles when a handler
depends on the catalog.

The container-free path remains complete: use `jobs.Define`, `jobs.NewCatalog`, `jobs.NewQueue`,
`jobs.On` and `jobs.NewWorkers`, then call `jobs.Enqueue` with the queue explicitly. This path is
also the right choice when one process intentionally hosts multiple independent queue runtimes.
The short `Automatic.Go`/`Binding.Go` form binds a package declaration to one active queue at a
time and rejects overlapping activations.

The PostgreSQL Fx application derives the default worker build ID from the executable content.
Set `Workers.Build` explicitly when a release changes job behavior through configuration without
changing the binary.

`WorkerObserver` is a trusted synchronous instrumentation boundary. Implementations must return
promptly and hand blocking export work to their own bounded transport; observer panics are
contained, but a blocking observer intentionally applies backpressure to the worker loop.

Compose several independent observers with `jobs.WorkerObservers` (or
`MustWorkerObservers`) rather than a chain of your own: it refuses more than
`MaxWorkerObservers` children at construction, skips nil and typed-nil entries,
runs children synchronously in registration order and isolates each child's
panic so the later ones still run. It starts no goroutine, queue, retry or
timer.

`Workers.Check(ctx) error` is the neutral readiness probe: it reports a latched
fatal driver failure, then the run state. It carries no importance, name or
status code — the composition root wraps it in the health contribution it chose.
See `[[D-096]]`.

## Enqueueing inside a transaction — this is the outbox

Give the driver the application's `crud.Source` and an enqueue made while a
transaction is open on that source is written **inside** that transaction:

```go
driver, err := jobspg.New(jobspg.Spec{DB: db, Source: source, /* ... */})

err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
	if _, err := contracts.Save(ctx, &agreement); err != nil {
		return err
	}
	_, err := jobs.Enqueue(ctx, queue, NotifyJob, NotifyPayload{ID: agreement.ID})
	return err
})
```

Commit and the job will run. Roll back and it never existed — no poller, no
`pending_effects` table, no second call at the commit point. That is the whole
of the outbox pattern, and it is why there is no separate outbox package here:
the invocation row *is* the durable delivery intent, and it is written by the
caller's own transaction.

`Enqueue` returns the `InvocationID` **before** the commit, so a row written in
the same transaction can name the effect it will cause.

Where the code already holds a `*sql.Tx` rather than a context binding, stage
explicitly:

```go
stager, err := driver.Stager(tx)
staged, err := jobs.EnqueueIn(ctx, queue, stager, NotifyJob, payload)
```

Two refusals keep the claim honest, and neither is a fallback:

- `jobspg.New` rejects a `Spec` whose `Source` is not the same data source as
  its `DB`. Atomicity across two handles is not a thing to promise.
- `Place` returns `jobs.ErrUnsupported` when the context carries an executor for
  that source which is not a transaction, or one no `*sql.Tx` can be taken from
  (a `crudpgx` executor, for instance). It never silently enqueues outside the
  caller's transaction instead.

A driver built **without** a `Source` has nothing to detect: every enqueue
commits on its own. That is a valid setup, but it is not the outbox, and the
call site looks identical either way — worth one assertion in the application's
own boot test.

## Publishing to a broker

There is no broker adapter in this repository and none is planned. A relay is
an ordinary job in your application:

```go
var PublishJob = jobsfx.AutoAdapterFor[*PublishHandler, PublishPayload](jobs.Interactive).
	JSON("integration.publish", 1)

func (h *PublishHandler) Handle(
	ctx context.Context,
	p PublishPayload,
	meta jobs.DeliveryMeta,
	_ jobs.AttemptController,
) error {
	return h.publisher.Publish(ctx, p.Topic, p.Body, meta.InvocationID().String())
}
```

The payload carries the routing key and the encoded message — not a Go value
only this build can decode — and the handler calls **your** port. Name that port
something of your own: `jobs.Sender` is the queue's backend seam, not a
publisher, and a second meaning for the word inside one subsystem is read
wrongly.

`AutoAdapterFor` rather than `AutoFor` is what gets the handler its
`DeliveryMeta`, and the last argument is the point: pass a deduplication key to
the broker. `meta.InvocationID()` and `meta.AttemptOrdinal()` are separate, and
the invocation is the one that is stable across attempts — its `String()` is a
canonical UUID, so it travels as a broker message id unchanged.

## What delivery promises, and what it does not

- **At-least-once.** A handler that finished its effect and lost its lease
  before recording that it had will run again — `ReasonLeaseLost` and
  `ReasonShutdown` are retry reasons, and neither is charged against the attempt
  budget. A crashed worker cannot be told from a slow one, so the framework
  redelivers rather than dropping. Make an effect that must not repeat
  idempotent, keyed on the invocation id.
- **No ordering.** Two jobs enqueued in one transaction may run in either order,
  concurrently, or minutes apart. A partition is a tenant (`PartitionGlobal`,
  `PartitionTenantRequired`), not a sequence; priority and retry backoff reorder
  on purpose. Nothing here is FIFO. Keep a sequence in your own rows if you need
  one.
- **Deduplicating a placement is not deduplicating a delivery.**
  `jobs.Unique(key)`, `jobs.Collapse(key)` and `EnqueueOnce` collapse two
  *enqueues* into one invocation. They say nothing about how many times that
  invocation reaches a handler, and nothing offers exactly-once.

Why: [[D-118]]. Where: [[FL-035]]. What a consumer may rely on: [[UC-031]].
