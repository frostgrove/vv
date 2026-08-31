package crudfiber

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func routed(err error) error {
	if _, isFault := errs.AsFault(err); isFault {
		return nil
	}
	var refusal *fiber.Error
	if !errors.As(err, &refusal) {
		return nil
	}
	return crudhttp.Routed(refusal.Code)
}

func Errors(options ...crudhttp.RenderOption) fiber.Handler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	return func(c fiber.Ctx) (err error) {
		defer func() {
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
		if refusal := routed(err); refusal != nil {
			return render(rd, c, refusal)
		}
		return render(rd, c, err)
	}
}

func ErrorHandler(options ...crudhttp.RenderOption) fiber.ErrorHandler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	return func(c fiber.Ctx, err error) error {
		if len(c.Response().Body()) > 0 {
			return nil
		}
		if refusal := routed(err); refusal != nil {
			return render(rd, c, refusal)
		}
		return render(rd, c, err)
	}
}
