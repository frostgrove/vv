package authfiber

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/vv/http/crudhttp"
)

// locale is the rendering context a refusal is written in.
//
// It reads the header rather than the fault, for port.WithLocale's reason: a
// fault crossing a queue must not carry the locale of the request that made it.
// First tag only — q-values pick between translations this library does not
// have.
func locale(c fiber.Ctx) context.Context {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return crudhttp.WithLocale(ctx, crudhttp.AcceptLanguage(c.Get("Accept-Language")))
}
