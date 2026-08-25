// Package remotehttp is the HTTP half of a call to a resource this process does
// not hold.
//
// It is the mirror of a binding: where crudnet turns a request into a command,
// this turns a [remote.Call] into a request, and it speaks exactly the routes
// every HTTP binding registers, so the service on the other end may be running
// Fiber, Gin or net/http.
//
// It reads the status table and the envelope backwards, and it reads porthttp's
// copy rather than its own. A client with its own table agrees with the server
// until the first time one of them gains a row, and the disagreement is a status
// silently reclassified ([[D-045]]).
//
// The gRPC transport is not here: it is in crud/rpc/crudgrpc, because moving it
// would cost a module for one file. The asymmetry is deliberate and [[D-058]]
// records it.
package remotehttp
