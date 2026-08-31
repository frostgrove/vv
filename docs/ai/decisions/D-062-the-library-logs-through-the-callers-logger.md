# D-062 — The library logs through the caller's logger, and instruments through the Source

**Status:** accepted
**Invariant:** This library never writes to a process-wide logger. Every line it emits goes through `port.Logger(ctx)`, which answers the context's `*slog.Logger` or `slog.Default()`. There is no logging option on any binding and no statement hook anywhere. A Source wrapper observes direct calls on that Source; an all-statements tracer belongs below transaction handles. It opts into a native storage effect only by implementing that exact unsafe capability itself.

## The decision

Two seams, for two different questions.

**"Tell me about the failures nobody can be returned an error for."** `port.Logger`
and `port.WithLogger`. Nine call sites across four transports and the shared
auth half: a handler panicked and the connection has to be closed, a response
would not marshal, a status could not carry its details, a refusal could not be
encoded or written.

**"Show me direct calls on this Source."** Wrap `crud.Source`. `crud.Executor`
is two methods, and a wrapper that also implements `crud.SourceUnwrapper`
preserves identity, replica and transaction-construction discovery ([[D-061]]).
The wrapper is not an all-statements tracer: a joined transaction and an owned
multi-statement transaction execute on their transaction handle. Instrument the
driver/connector, or make `Begin` return an instrumented `Tx`, when those calls
must also be visible. Native storage effects do not tunnel through an unknown
wrapper; it must explicitly implement and instrument `crud.UnsafeBulkInserter`.

## Why

**Because `log.Printf` writes to the standard library's package logger, which
belongs to the whole process.** A consumer could not give these lines the
request's trace id, could not route them to their own handler, and could not
silence them without silencing every other library in the binary that reached for
the same default. That is the library taking a decision that is the
application's.

**Because a logger option on each binding would be a fourth copy of the same
field.** There are four transports; `MaxBody` had to be written four times to
exist at all, which is what [[D-045]] and `port.Rules` are about. A context value
is the one shape that needs no per-binding wiring, and it is also the shape that
carries the request's own fields, which a handler-scoped logger would not.

**Because `slog` and not an interface of our own.** An interface would be a
contract-manifest entry, and [[D-048]] says a package joins the manifest only when
a second implementation asks and never when the standard library already
contracts it. `slog.Handler` is that contract. It is also what a consumer already
has.

**Because the direct-call seam already existed and needed documenting, not
building.** A `crud.Source` is `Exec`, `Query` and `Dialect`. Wrapping it is a
small way to time or guard calls made on that handle, and [[D-061]] keeps replica,
identity and transaction construction discoverable. It cannot generically
retarget the wrapper to a transaction returned by the wrapped source. Driver or
connector instrumentation sits below both handles and is therefore the default
for complete tracing. A hook on `sqlrepo.Setting` would still be a second,
weaker version visible only to `sqlrepo`.

**Because native bulk is an effect rather than discovery.** Automatically
walking through a wrapper to reach pgx COPY would make the faster path invisible
to precisely the tracing/rate-limit/circuit-breaker wrapper the application
installed. The safe default under an unknown wrapper is the portable SQL path:
its `Exec` sees a direct one-statement plan. A multi-statement plan keeps
atomicity by using the transaction handle, so observing every chunk requires
transaction-aware or driver-level instrumentation. A wrapper that deliberately
preserves native bulk implements `UnsafeBulkInsert` itself and records/guards
that call before forwarding. `ReadWrite` does so only to route the effect to its
primary.

**Because "no logger" must not be a nil check at nine call sites.**
`port.Logger` never returns nil and `port.WithLogger(ctx, nil)` stores nothing.

## What it forbids

- Do not call `log.Printf`, `fmt.Println` or `os.Stderr` from library code.
- Do not add a `WithLogger` option to a binding. The context is the seam.
- Do not log anything a client is not allowed to see and then also render it.
  These lines exist precisely because the response may not carry the cause
  ([[D-044]]).
- Do not add a statement hook to `sqlrepo`. Wrap the `Source`.
- Do not present a Source wrapper as an all-statements tracer. Joined and owned
  transaction handles need driver-level instrumentation or an explicitly
  instrumented `Begin`/`Tx`.
- Do not use `SourceUnwrapper` as consent to execute an effect underneath an
  instrumentation wrapper. Forward `UnsafeBulkInsert` explicitly or select the
  portable SQL fallback; its transaction visibility follows the rule above.

## Where it lives

- `port/log.go` — `Logger`, `WithLogger`.
- `crud/http/crudnet/middleware.go`, `crud/http/crudgin/middleware.go`,
  `crud/http/crudfiber/middleware.go` — the panic line.
- `crud/http/crudnet/options.go`, `crud/http/crudgin/options.go`,
  `crud/http/crudfiber/options.go` — the response that would not marshal. All
  three carry the line; it was one binding until the marshal-first guard reached
  the other two.
- `crud/rpc/crudgrpc/status.go` — the details that would not attach.
- `auth/http/authhttp/authhttp.go` — the refusal that would not encode or write.
- `crud/executor.go:SourceUnwrapper`, `UnsafeBulkInserterOf` — the statement
  seam and its explicit effect boundary.

## Proven by

- `TestTheLibrarysOwnLinesGoWhereTheApplicationSays` in `port/log_test.go` — the
  line reaches the application's handler carrying that logger's fields, with two
  controls: a context with no logger answers something else and never nil, and
  `WithLogger(ctx, nil)` does not store one.
- `TestARefusalThatWillNotEncodeIs500AndSaysNothing` in
  `auth/http/authhttp/refuse_test.go` — one of those lines at its call site: the
  refusal that would not marshal reaches the logger the request carried, which
  is also what makes the silent body provable. Verified by rendering that line
  through `context.Background()` and watching the capture come back empty.
- `TestAWrappedSourceKeepsWhatItWrapsWhenItSaysWhatItWraps` in
  `crud/wrapsource_test.go` — the instrumentation seam, driven by a wrapper that
  counts statements and does not accidentally inherit native bulk.
- `TestUnknownSourceWrapperSeesSingleStatementPortableSQL` in
  `crud/sqlrepo/insert_batch_test.go` — a direct one-statement InsertBatch uses
  the wrapper's `Exec` unless it explicitly publishes the native effect.

## See also

[[D-021]] [[D-042]] [[D-044]] [[D-045]] [[D-048]] [[D-061]]
