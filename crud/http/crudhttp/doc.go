// Package crudhttp is what is HTTP *and* CRUD — the small remainder after two
// splits, and the compatibility surface over both of them.
//
// A binding — crudfiber, crudgin, crudnet, or one you write — owns routing, body
// decoding and how a response is written. What is left divides three ways:
//
//   - In port: the commands, the Service, the Mapper, the code vocabulary, the
//     violations pipeline and the field clearing a create request is not allowed
//     to dictate. None of it is HTTP ([[D-045]]).
//   - In port/porthttp: the error-to-status table and its inverse, the response
//     envelope and its parser, the Renderer seam, the JSON body decode, the
//     locale, and the raw-body fallback that turns a column name back into the
//     key the client sent. All of it is HTTP and none of it is CRUD — the auth
//     middleware answers a 401 through the same table ([[D-059]]).
//   - Here: the request shapes a CRUD route has and nothing else does —
//     BulkDeleteRequest, CoerceID, the count and entity narrowing — and the
//     create-time model clearing.
//
// Two tests, asked in that order. Could a non-HTTP transport implement this
// without importing net/http? If not, it is not port's — a Renderer returning an
// http.Header could not, which is why the seam is not in errs. Could a subsystem
// that is not CRUD take this without importing crud? If yes, it is porthttp's.
//
// Every symbol this package used to own that moved is still exported from here,
// as an alias or a one-line forwarder — see porthttp.go. Re-pointing an alias is
// not a breaking change, and that is how [[D-034]] landed before it.
package crudhttp
