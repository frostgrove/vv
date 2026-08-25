// Package crudhttp is the HTTP half of a transport binding — the part that has
// a framework in it nowhere, and net/http in it everywhere.
//
// A binding — crudfiber, crudgin, crudnet, or one you write — owns routing,
// body decoding and how a response is written. What is left divides in two, and
// [[D-045]] is where the line is drawn:
//
//   - Here: the error-to-status table, the response envelope, the Renderer
//     seam, and the raw-body fallback that turns a column name back into the
//     key the client sent. Every one of them is shaped by HTTP.
//   - In port: the commands, the Service, the Mapper, the code vocabulary, and
//     the field clearing a create request is not allowed to dictate. None of
//     them is.
//
// The test for which side something belongs on is whether a gRPC binding could
// implement it without importing net/http. A Renderer returning an http.Header
// could not, which is why it is here and not in errs.
//
// Every symbol this package used to own that moved to port is still exported
// from here, as an alias or a one-line forwarder. Re-pointing an alias is not a
// breaking change, and that is how [[D-034]] landed before it.
package crudhttp
