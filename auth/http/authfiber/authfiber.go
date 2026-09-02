package authfiber

import (
	"bytes"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

func Middleware(guard *auth.Guard, options ...porthttp.RenderOption) fiber.Handler {
	if err := guard.Validate(); err != nil {
		panic("authfiber: Middleware needs a ready Guard: " + err.Error())
	}
	renderer := authhttp.RendererFor(options)
	return func(fiberContext fiber.Ctx) error {
		ctx, err := guard.AuthenticateValues(
			fiberContext.Context(),
			func(name string) []string { return headerValues(fiberContext, name) },
		)
		if err != nil {
			return refuse(fiberContext, renderer, err)
		}
		fiberContext.SetContext(ctx)
		return fiberContext.Next()
	}
}

func headerValues(c fiber.Ctx, name string) []string {
	var values []string
	needle := []byte(name)

	for key, value := range c.Request().Header.All() {
		if bytes.EqualFold(key, needle) {
			values = append(values, string(value))
		}
	}
	return values
}

func refuse(c fiber.Ctx, rd porthttp.Renderer, err error) error {
	status, header, body := rd.Render(locale(c), err)
	for name, values := range header {
		for _, value := range values {
			c.Response().Header.Add(name, value)
		}
	}
	if body == nil {
		return c.SendStatus(status)
	}
	return c.Status(status).JSON(body)
}

func SkipPreflight(handler fiber.Handler) fiber.Handler {
	return AnswerPreflight(handler, func(fiberContext fiber.Ctx) error {
		return fiberContext.SendStatus(authhttp.PreflightStatus)
	})
}

func AnswerPreflight(handler fiber.Handler, preflight fiber.Handler) fiber.Handler {
	if preflight == nil {
		panic("authfiber: AnswerPreflight needs something to answer a preflight with")
	}
	return func(fiberContext fiber.Ctx) error {
		if authhttp.Preflight(fiberContext.Method(), func(name string) string { return fiberContext.Get(name) }) {
			return preflight(fiberContext)
		}
		return handler(fiberContext)
	}
}
