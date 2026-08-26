package crudfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

// Errors renders whatever a handler returned, for handlers this library did not
// write.
//
// Fiber handlers return an error, so the decorator is a wrapper around c.Next()
// — the shape the other two bindings do not have. [ErrorHandler] is the other
// seam and the more natural one for an application that builds its own app.
//
// A handler that already wrote a response is left alone: writing a second body
// produces a corrupt one. And it is safe to install twice — once on the app and
// once on a group is the ordinary way that happens; the inner copy renders and
// returns nil, so the outer copy has nothing to render.
func Errors(opts ...crudhttp.RenderOption) fiber.Handler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(opts) > 0 {
		rd = crudhttp.NewRenderer(opts...)
	}
	return func(c fiber.Ctx) (err error) {
		defer func() {
			// A renderer bug must not become a dropped connection. If nothing
			// has been written the client still gets the silent 500; if
			// something has, the status is already gone and there is nothing to
			// do but log.
			if p := recover(); p != nil {
				port.Logger(c.Context()).Error("crudfiber: panic while serving a request",
					"method", c.Method(), "path", c.Path(), "panic", p)
				err = nil
				if len(c.Response().Body()) == 0 {
					err = c.Status(fiber.StatusInternalServerError).JSON(crudhttp.Internal())
				}
			}
		}()
		if err = c.Next(); err == nil {
			return nil
		}
		if len(c.Response().Body()) > 0 {
			return nil
		}
		return render(rd, c, err)
	}
}

// ErrorHandler is the same rendering as Fiber's own app-level seam:
//
//	fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler()})
//
// Every handler in the app then answers failures the way the CRUD routes do,
// with nothing wrapped around anything.
func ErrorHandler(opts ...crudhttp.RenderOption) fiber.ErrorHandler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(opts) > 0 {
		rd = crudhttp.NewRenderer(opts...)
	}
	return func(c fiber.Ctx, err error) error {
		if len(c.Response().Body()) > 0 {
			return nil
		}
		return render(rd, c, err)
	}
}
