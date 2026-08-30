// Package authnet authenticates a net/http request.
//
// The whole set-up is one line:
//
//	mux.Handle("/articles/", authnet.Middleware(guard)(articles))
//
// or, over a whole server:
//
//	http.ListenAndServe(":8080", authnet.Middleware(guard)(mux))
//
// It puts an [auth.Principal] into the request context, which is the only
// channel that reaches a repository: a transport hook can reject a request but
// cannot rewrite the context the repository sees, so a principal left in a
// framework's own per-request store is invisible to every policy.
//
// It is in the root module because it imports only the standard library. A
// second `go get` bought for no dependency is a cost with nothing on the other
// side of it, which is the same reason crudnet and crud/adapter/crudsql are here
// ([[D-033]]).
//
// Everything this package does that is not reading a header and writing a
// refusal comes from auth.Guard and authhttp, so the four transports cannot
// drift apart on whether an optional guard accepts a forged token ([[D-045]]).
package authnet

import (
	"net/http"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

// Middleware authenticates every request that passes through it.
//
// A refusal is written here and the next handler never runs. It renders through
// the same envelope as every other failure, so a client sees one error shape
// whether the request was refused at the door or by the repository.
//
// Consecutive installs with the same [auth.Guard] authenticate once. A
// different guard performs its own check; A -> B -> A fails closed because no
// assurance order is inferred ([[D-076]]).
func Middleware(guard *auth.Guard, options ...porthttp.RenderOption) func(http.Handler) http.Handler {
	if err := guard.Validate(); err != nil {
		panic("authnet: Middleware needs a ready Guard: " + err.Error())
	}
	renderer := authhttp.RendererFor(options)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := guard.AuthenticateValues(r.Context(), r.Header.Values)
			if err != nil {
				authhttp.Refuse(w, r, renderer, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Handler is [Middleware] applied to one handler, for a route that is
// authenticated where its neighbours are not.
func Handler(guard *auth.Guard, next http.Handler, options ...porthttp.RenderOption) http.Handler {
	return Middleware(guard, options...)(next)
}
