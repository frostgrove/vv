# Application-owned native OpenTelemetry recipes

This is an unpublished integration/test module, not a Frostgrove package. Copy
only the recipe files for the contrib libraries used by the application. The
application owns the SDK providers, exporters, Views, projection chain and
lifecycle; `vvotel` only receives the resulting providers.

## Assembly

Construct closed route, RPC, resource and pool-name tables first. Wrap the raw
trace exporter with the database and transport span projectors. Wrap the raw
metric exporter with runtime, database and transport metric projectors. Supply
`TransportMetricOptions`, `DatabaseMetricOptions` and `RuntimeMetricOptions`
when constructing the one `sdkmetric.MeterProvider`; Views cannot be added
after construction.

Pass the resulting `Providers` explicitly to `HTTPServer`, `HTTPTransport`,
`GinMiddleware`, `FiberMiddleware`, `GRPCServerStats`, `GRPCClientStats`,
`NewPGXTracer`, `OpenSQL`/`OpenSQLDB`, `RegisterSQLDBStats`,
`RegisterPGXPoolStats` and `StartRuntimeMetrics`. No recipe changes an OTel
global. Unregister DB/pool callbacks before the final metric flush and provider
shutdown. [`full_stack_test.go`](full_stack_test.go) is the smallest shared-SDK
HTTP → Frost command → Frost CRUD → native SQL composition proof.

`HTTPServer` includes `/live` and `/ready` transport signals by default. Passing
`ExcludeManagementSignals()` is the explicit both-signals variant: it suppresses
native tracing and metrics only when the sealed mux resolves the exact path-only
pattern `/live` or `/ready`. Method-qualified, host-qualified, redirected and
unmatched requests remain instrumented. For the default path, `HTTPServer` adds
the bounded metric-only attribute `vv.http.management="true"` only to those
exact handlers. Collector/backend filters and HTTP SLO rules use that marker,
not the lossy upstream `http.route` projection, so request spans and probe
parentage remain intact.

The projection delegates are required, not optional cleanup. Constructor
options reduce capture, Views bound in-process metric series, and the exporter
projection is the final allowlist for OTLP, including metric exemplar filtered
attributes.

## Privacy and cardinality enforcement

| Integration and source | Exposure risk | Constructor prevention | SDK View | Export projection | Canary |
|---|---|---|---|---|---|
| net/http server: URL, query, headers, peer/user-agent, route | secret or unbounded request data | sealed `HTTPRoutes`, fixed span-name formatter, explicit TraceContext propagator | method/status/exact sealed route only | allowlisted method/status/route; name is sealed route or `_OTHER` | `http_test.go`, `projection_test.go` |
| net/http client: full URL, query, headers, peer, user-agent | credentials and unbounded destinations | fixed method-only span-name formatter | method/status only | drops URL/query/header/peer fields; bounded `HTTP <METHOD>` name | `http_test.go`, `projection_test.go` |
| Gin server: request fields and router path | request secrets and route cardinality | immutable `RouteTable`, scoped provider and formatter | method/status/exact route only | same transport allowlist and fallback | `gin_test.go`, `metric_views_test.go` |
| Fiber server: request fields and Basic Authorization username (`enduser.id`) | credential-derived identity | client IP disabled; immutable `RouteTable`; pinned middleware has no switch for username extraction | method/status/exact route; active requests keep method only | drops `enduser.id` and all non-allowlisted fields | `fiber_test.go`, `projection_test.go` |
| gRPC server/client: full method, metadata, status text | metadata credentials and unbounded method/status text | immutable `RPCTable`, bounded stats handler, TraceContext-only propagator | system/method/closed status only | exact method or `_OTHER/_OTHER`; drops metadata, events and status description | `grpc_test.go`, `projection_test.go` |
| public HTTP/gRPC TraceContext | hostile tracestate and causal spoofing | `PublicIngress` creates a new root and sanitized remote link | not a metric dimension | preserves IDs/flags only; drops tracestate and link attributes | `public_context_test.go` |
| trusted HTTP/gRPC context and baggage | accidental baggage propagation | TraceContext-only propagator; no composite baggage propagator | not a metric dimension | sanitized exported span/link contexts contain no tracestate | `trusted_test.go`, `projection_test.go` |
| pgx SQL and parameters | SQL literals, binds and connection details | SQL statement and connection details disabled; fixed scoped tracer and name function | closed system/operation attributes | drops SQL/args; normalizes operation name | `db_pgx_test.go`, `db_runtime_projection_test.go` |
| pgx COPY table and Prepare statement name | collection/table and prepared-name cardinality | generic name function; upstream can still create these fields | closed system/operation attributes | drops `db.collection.name` and `pgx.prepare_stmt.name`; normalizes names | `db_pgx_test.go`, `db_runtime_projection_test.go` |
| `database/sql`: query, DSN and arbitrary driver error | statement, credential and error-text leakage | `DisableQuery`, SQL commenter off, fixed name formatter and explicit connector/options | closed operation; configured pool only | exports no SQL-span attributes/events/status description; closed operation name | `db_sql_test.go`, `db_runtime_projection_test.go` |
| SQL DB stats | pool-name cardinality and callback lifetime | validated configured `DatabasePoolName`; guarded registration | configured pool/status only | exact scope/instruments/pool/status; exemplar attributes projected too | `db_stats_test.go`, `db_runtime_views_test.go` |
| pgxpool stats | pool-name cardinality and retained pool handle | validated configured `DatabasePoolName`; explicit unregister handle | configured pool only | exact custom scope/instrument roster and pool value | `db_stats_test.go`, `db_runtime_views_test.go` |
| Go runtime | unexpected resource/scope identity or new labels after dependency upgrades | explicit one-time start per provider | exact runtime scope; only closed `go.memory.type` where applicable | fixed eight-instrument roster; all other attributes dropped | `runtime_metrics_test.go`, `db_runtime_projection_test.go` |
| every native metric | exemplar filtered attributes bypassing a View | capture options above | scope/instrument-specific key filter | datapoints and exemplars are deep-copied and projected | `projection_test.go`, `db_runtime_projection_test.go` |

`native_budget_manifest.json` pins module/semconv mode, resource, scope,
instrument metadata, complete attribute domains and independent series ceilings.
`make check-otel-native-budget` exercises real integrations and rejects missing
or unknown roster entries, values and over-budget series.

The advanced pin review also checked `otelsql v0.44.0`. It still creates the
duration recorder from the incoming context before the DB span, so its duration
exemplar identifies the immediate caller span. The recipe stays on the tested
`v0.43.0`/OTel `v1.44.0` stack and asserts that behavior instead of guessing a
DB child across concurrent calls; `v0.44.0` additionally requires OTel `v1.46.0`.
