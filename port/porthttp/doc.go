// Package porthttp is the HTTP projection of the error contract, and it belongs
// to no subsystem.
//
// The status table and its inverse, the response envelope and its parser, the
// Renderer seam, the JSON body decode, the retained body, the locale and the
// raw-body path fallback. Every subsystem that answers a failure over HTTP
// answers through this one: crudnet renders a 409, authnet renders a 401, and
// the client in remotehttp reads both of them back.
//
// It is a cell in the same grid every other package name comes from — subsystem
// port, library net/http — exactly as crudhttp is crud × net/http ([[D-035]]).
//
// Two lines drawn one after the other put it here. The first is [[D-045]]'s: a
// Renderer answers an http.Header, so gRPC cannot implement it and it is not
// port's. The second is [[D-059]]'s: the auth middleware needs the same
// Renderer, and taking it from crudhttp made a token check depend on the SQL
// repository, the predicate AST and an HTTP client to another service.
//
// Not in errs, for a reason that is about release rather than shape: errs is
// sealed with an empty require block until its own tag, and half of what is here
// calls port.KindOf, port.Violations and port.FirstLanguageTag.
//
// crudhttp re-exports all of it, so nothing written against the old names broke.
package porthttp
