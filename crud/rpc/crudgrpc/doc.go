// Package crudgrpc mounts a full CRUD API on gRPC.
//
// The whole set-up is one line:
//
//	crudgrpc.New(articles).Register(server, "Article")
//
// where `articles` is anything satisfying Repository — a crud.Repo, a
// specs.Repo, or your own service struct that embeds one and adds business
// rules. The handler never reaches past that interface, so a service layer
// slots in without the handler noticing.
//
// Methods, one per port command:
//
//	List        the query document -> a page
//	Count       the query document -> {"count": n}
//	Get         {"id": "42", "query": {…}} -> the entity
//	Create      the entity document -> the entity
//	Update      {"id": "42", "patch": {…}} -> the entity
//	Replace     {"id": "42", "entity": {…}} -> the entity
//	Delete      {"id": "42"} -> {"deleted": n}
//	BulkDelete  {"ids": ["1","2"]} -> {"deleted": n}
//
// Everything this package does that is not routing, decoding or writing a
// response comes from port: the commands, the service, the field clearing, the
// kind that decides the response and the violation pipeline that fills its
// details ([[D-045]]). Nothing here re-derives a code, a kind or a path hop.
// This package is what phase 9 measured that claim with: adding it changed no
// line of errs, errs/sqlerr or port.
//
// # What is different from the three HTTP bindings
//
// The renderer is not shared. crudhttp's Renderer answers
// (int, http.Header, any), which gRPC cannot implement, so this package has a
// [Renderer] of its own that answers a *status.Status. What both renderers
// build from is the same: port.Violations, one pipeline, one order, one cap,
// one message ladder. [[FL-013]] carries the whole difference table.
//
// Four limits, stated rather than left to be discovered.
//
// **There is no schema for a resource.** A repository is generic over its
// model, so no compiled proto message for it can exist in a library. Every
// request and every response is a google.protobuf.Struct carrying the same JSON
// document the HTTP bindings speak, which is what lets one service value serve
// all four transports and answer the same thing. See [[D-052]].
//
// **Server reflection cannot describe the service.** A generic resource has no
// file descriptor, so grpcurl and its kind cannot list the methods. Clients
// call by full method name, or the application registers a descriptor it
// generated itself.
//
// **A number in a Struct is a double.** google.protobuf.Value has no integer,
// so it cannot carry every int64 exactly. The API treats integral values at
// magnitude 2^53 and beyond as outside its safe Struct range. An entity
// document that needs them declares `json:"id,string"`. Keys do not: a keyed request
// carries id as a string, and a bulk
// request carries ids as strings. port.CoerceID converts them the same way the
// HTTP path parameter is converted. The Remote gRPC client automatically turns
// numeric bulk keys into their exact decimal-string spelling. A raw Struct
// caller must do the same for an unsafe id or ids member.
//
// The query door refuses an unsafe integral Struct number rather than rounding
// it. For an integral filter operand — including one in a preload filter — send
// the exact decimal value as a JSON string; ordinary query coercion restores
// the declared column type. `page`, `limit`, `offset` and `preload.maxRows` are
// controls, not typed operands: they must be exact JSON integers and cannot use
// the string recovery spelling. `count`, `deleted`, `total` and `totalPages`
// answer as JSON numbers in the exact range and as decimal strings outside it.
//
// **There is no raw-body fallback.** The HTTP bindings keep the decoded request
// bytes so a violation on a column nothing declared can still name the key the
// client sent. Here the declared hops — the service's and the mapper's — are
// the whole chain, and a path nothing owns is marked approximate rather than
// guessed ([[D-043]]).
//
// # Four constructors, as in every other binding
//
// New takes a repository and builds the default service. NewFor takes a mapper
// when the request document is not the model's own JSON shape. Serving and
// ServingFor mount a port.Service that is already built, which is how one
// service value serves Fiber, Gin, net/http and gRPC at once.
//
// There is no WithErrorHandler. A gRPC response is a return value rather than a
// stream a handler may have half-written, so there is nothing for a handler to
// take over: [WithRenderer] is the whole seam, and [Errors] is the same seam
// for the methods this package did not write.
package crudgrpc
