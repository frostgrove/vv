// Package port is the half of a transport binding that has no transport in it.
//
// A binding — crudfiber, crudgin, crudnet, or a gRPC one — owns exactly three
// things: which routes exist, how a body becomes a Go value, and how a response
// is written. Everything else is here: the commands a route builds, the Service
// those commands are handed to, the Mapper that turns a transport input type
// into the model, and the vocabulary that decides what an error is
// ([[D-045]]).
//
// The chain is
//
//	handler -> generic transport layer -> Mapper -> Service -> Repository
//
// and the path chain runs back the other way, one hop per layer, nothing
// guessed ([[D-043]]).
//
// # What is not here
//
// The status table, the response envelope and the Renderer seam stay in
// http/crudhttp. The test for the shared half is whether a non-HTTP transport
// can implement it without importing net/http, and a renderer returning an
// http.Header fails it. Something HTTP-shaped wearing a neutral name is worse
// than an honest HTTP package.
//
// # Four questions this package was asked before it was written
//
// Its scope is request/response transports: three HTTP bindings and gRPC. A
// queue consumer has no request, no id in a path and no status to map, so it
// calls a Service directly and never builds a command.
//
// There is no fourth type parameter on New. Go has no default type
// arguments, so adding In to New[M, ID, U] breaks inference at every existing
// call site, which [[D-022]] forbids. The answer is a second constructor:
// New means "the body binds onto the model" and NewFor names a distinct input
// type. Both live in the bindings; this package supplies the Mapper the second
// one takes.
//
// A default implementation beside the contract does not disqualify it.
// DefaultService ships here, and [[D-048]]'s rule is a count of
// implementations rather than a judgement about purity: three transports and a
// fourth protocol are what earn `port` its place on the manifest.
//
// PATCH still decodes straight into U. Mapper covers the entity body only.
// A transport-specific patch shape would be a fifth type parameter, and the
// generated DTO already is the transport shape ([[D-018]]), so it costs nothing
// today. It is a stated limit, not an oversight.
//
// # One more limit worth stating
//
// Fields passes an undeclared head through rather than declining. A declining
// hop poisons errs.Chain and would mark approximate a violation the raw-body
// index resolves today. Strictness belongs to PathMap, the generated map: it is
// total by construction, checked against the model at package initialisation,
// and refuses to start when a column is missing ([[D-050]]). That asymmetry is
// deliberate and is the difference between a map somebody typed and a map a
// generator owes the model.
package port
