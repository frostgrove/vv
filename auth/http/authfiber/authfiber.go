// Package authfiber authenticates a Fiber v3 request.
//
// The whole set-up is one line:
//
//	app.Use(authfiber.Middleware(guard))
//
// or on one group:
//
//	api := app.Group("/api", authfiber.Middleware(guard))
//
// # The one thing that is different here
//
// The principal goes into the context with c.SetContext, not into Locals.
//
// Locals is where a Fiber middleware normally puts per-request state, and it is
// the wrong place for this: crudfiber hands c.Context() down to the port layer,
// so a principal in Locals is invisible to every policy. Both spellings
// compile, both look right in a review, and only one of them narrows a query.
//
// Everything else comes from auth.Guard and authhttp, so the four transports
// cannot drift apart on whether an optional guard accepts a forged token
// ([[D-045]]).
package authfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

// Middleware authenticates every request that passes through it.
//
// A refusal is written here rather than returned, for the reason authhttp's
// documentation gives: a consumer who mounts this without also mounting
// crudfiber.Errors would otherwise get an empty 200 for every unauthenticated
// request. crudfiber.Errors and crudfiber.ErrorHandler both leave an
// already-written response alone, so this composes with either.
//
// Installing it twice authenticates once: [auth.Guard] hands back a context
// that already carries a principal untouched.
func Middleware(g *auth.Guard, opts ...porthttp.RenderOption) fiber.Handler {
	if g == nil {
		panic("authfiber: Middleware needs a Guard; without one nothing is authenticated")
	}
	rd := authhttp.RendererFor(opts)
	return func(c fiber.Ctx) error {
		// c.Get is variadic in Fiber v3, so it is adapted rather than passed:
		// the Guard takes the one shape every transport can supply.
		ctx, err := g.Authenticate(c.Context(), func(name string) string { return c.Get(name) })
		if err != nil {
			return refuse(c, rd, err)
		}
		c.SetContext(ctx)
		return c.Next()
	}
}

// refuse writes the 401 through Fiber's own writer. It is the four lines
// authhttp.Refuse cannot do here, because Fiber has no http.ResponseWriter.
func refuse(c fiber.Ctx, rd porthttp.Renderer, err error) error {
	status, header, body := rd.Render(locale(c), err)
	for k, vs := range header {
		for _, v := range vs {
			c.Set(k, v)
		}
	}
	if body == nil {
		return c.SendStatus(status)
	}
	return c.Status(status).JSON(body)
}
