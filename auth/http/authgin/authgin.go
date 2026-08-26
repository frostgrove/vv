// Package authgin authenticates a Gin request.
//
// The whole set-up is one line:
//
//	r.Use(authgin.Middleware(guard))
//
// or on one group:
//
//	api := r.Group("/api", authgin.Middleware(guard))
//
// It puts an [auth.Principal] into the request context — c.Request's context
// and not c.Set, because that is the only one a repository sees. A principal in
// Gin's own key store is invisible to every policy, and both would compile.
//
// Everything this package does that is not reading a header and writing a
// refusal comes from auth.Guard and authhttp, so the four transports cannot
// drift apart on whether an optional guard accepts a forged token ([[D-045]]).
package authgin

import (
	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

// Middleware authenticates every request that passes through it.
//
// A refusal is written here and c.Abort() stops the chain. It renders through
// the same envelope as every other failure, so a client sees one error shape
// whether the request was refused at the door or by the repository — and
// crudgin.Errors, if it is also installed, leaves an already-written response
// alone.
//
// The error is also filed with c.Error, so Gin's own logging middleware sees
// the cause the body deliberately does not carry.
//
// Installing it twice authenticates once: [auth.Guard] hands back a context
// that already carries a principal untouched.
func Middleware(g *auth.Guard, opts ...porthttp.RenderOption) gin.HandlerFunc {
	if g == nil {
		panic("authgin: Middleware needs a Guard; without one nothing is authenticated")
	}
	rd := authhttp.RendererFor(opts)
	return func(c *gin.Context) {
		ctx, err := g.Authenticate(c.Request.Context(), c.GetHeader)
		if err != nil {
			_ = c.Error(err)
			authhttp.Refuse(c.Writer, c.Request, rd, err)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
