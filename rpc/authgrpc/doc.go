// Package authgrpc authenticates a gRPC call.
//
// The whole set-up is one line:
//
//	srv := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard)),
//	)
//
// It puts an [auth.Principal] into the call's context, which is the only thing
// a gRPC method has — there is no request object here, and crudgrpc's own hooks
// take a context for the same reason.
//
// # What is different from the three HTTP bindings
//
// A credential comes from metadata, not from a header. The keys are the same
// names lowercased, because that is what gRPC does to them, and md.Get is
// already case-insensitive — so [Unary] hands auth.Guard a getter over metadata
// and every decision above it is shared ([[D-045]]).
//
// There is no 404 here and no status table of this package's own. A refusal is
// an error returned from the interceptor; crudgrpc.Errors renders it, and
// google.rpc.Code.UNAUTHENTICATED is what errs.KindUnauthorized already maps to.
// So this package writes no status and the ordering in [[D-008]] is untouched.
//
// [Skip] keys on the full method name — "/vv.crud.v1.Article/Create" — which is
// the reason crudgrpc gives a resource its own service name rather than sharing
// one. Under a shared service every resource's Create is the same method and no
// rule could name one of them.
//
// # Two limits, stated rather than left to be discovered
//
// A stream is authenticated once, when it opens. A credential that expires
// mid-stream is not noticed; a long-lived stream that must re-check does it in
// its own loop, because an interceptor has no seam for it.
//
// The interceptor does not read the peer's TLS certificate. mTLS is a different
// authentication and its principal comes from credentials.AuthInfo rather than
// from metadata; wire it as an [auth.Authenticator] of your own and pass it to
// the same guard.
package authgrpc
