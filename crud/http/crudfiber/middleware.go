package crudfiber

import (
	"context"
	"errors"
	"reflect"

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
	baseOptions := append([]crudhttp.RenderOption(nil), options...)
	rd := crudhttp.Renderer(defaultRenderer)
	if len(baseOptions) > 0 {
		rd = crudhttp.NewRenderer(baseOptions...)
	}
	return func(c fiber.Ctx) (err error) {
		if errorsInstalled(c.Context()) {
			return c.Next()
		}
		c.SetContext(withErrors(c.Context()))
		defer func() {
			if p := recover(); p != nil {
				port.Logger(c.Context()).ErrorContext(c.Context(), "crudfiber: panic while serving a request",
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
			return render(installedRenderer(rd, baseOptions, resourceRenderingFrom(c.Context())), c, refusal)
		}
		return render(installedRenderer(rd, baseOptions, resourceRenderingFrom(c.Context())), c, err)
	}
}

type errorsKey struct{}

func withErrors(ctx context.Context) context.Context {
	return context.WithValue(ctx, errorsKey{}, true)
}

func errorsInstalled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	installed, _ := ctx.Value(errorsKey{}).(bool)
	return installed
}

func processErrorHandlerInstalled(c fiber.Ctx) bool {
	handler := c.App().Config().ErrorHandler
	return handler != nil && reflect.ValueOf(handler).Pointer() != reflect.ValueOf(fiber.DefaultErrorHandler).Pointer()
}

type renderingKey struct{}

type resourceRendering struct {
	renderer crudhttp.Renderer
	standard bool
	options  []crudhttp.RenderOption
}

func withResourceRendering(ctx context.Context, rendering resourceRendering) context.Context {
	rendering.options = append([]crudhttp.RenderOption(nil), rendering.options...)
	return context.WithValue(ctx, renderingKey{}, rendering)
}

func resourceRenderingFrom(ctx context.Context) resourceRendering {
	if ctx == nil {
		return resourceRendering{}
	}
	rendering, _ := ctx.Value(renderingKey{}).(resourceRendering)
	rendering.options = append([]crudhttp.RenderOption(nil), rendering.options...)
	return rendering
}

func installedRenderer(base crudhttp.Renderer, baseOptions []crudhttp.RenderOption, resource resourceRendering) crudhttp.Renderer {
	if resource.renderer != nil {
		return resource.renderer
	}
	if !resource.standard || len(resource.options) == 0 {
		return base
	}
	options := make([]crudhttp.RenderOption, 0, len(baseOptions)+len(resource.options))
	options = append(options, baseOptions...)
	options = append(options, resource.options...)
	return crudhttp.NewRenderer(options...)
}

func ErrorHandler(options ...crudhttp.RenderOption) fiber.ErrorHandler {
	baseOptions := append([]crudhttp.RenderOption(nil), options...)
	rd := crudhttp.Renderer(defaultRenderer)
	if len(baseOptions) > 0 {
		rd = crudhttp.NewRenderer(baseOptions...)
	}
	return func(c fiber.Ctx, err error) error {
		if len(c.Response().Body()) > 0 {
			return nil
		}
		renderer := installedRenderer(rd, baseOptions, resourceRenderingFrom(c.Context()))
		if refusal := routed(err); refusal != nil {
			return render(renderer, c, refusal)
		}
		return render(renderer, c, err)
	}
}
