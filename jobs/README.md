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
