package authfiber

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

func locale(c fiber.Ctx) context.Context {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if port.LocaleFrom(ctx) != "" {
		return ctx
	}
	return porthttp.WithLocale(ctx, porthttp.AcceptLanguage(c.Get("Accept-Language")))
}
