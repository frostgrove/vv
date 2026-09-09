package crudgin

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

func Errors(options ...crudhttp.RenderOption) gin.HandlerFunc {
	baseOptions := append([]crudhttp.RenderOption(nil), options...)
	rd := crudhttp.Renderer(defaultRenderer)
	if len(baseOptions) > 0 {
		rd = crudhttp.NewRenderer(baseOptions...)
	}
	return func(c *gin.Context) {
		if errorsInstalled(c.Request.Context()) {
			c.Next()
			return
		}
		c.Request = c.Request.WithContext(withErrors(c.Request.Context()))
		defer func() {
			if p := recover(); p != nil {
				port.Logger(c.Request.Context()).ErrorContext(c.Request.Context(), "crudgin: panic while serving a request",
					"method", c.Request.Method, "path", c.Request.URL.Path, "panic", p)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, crudhttp.Internal())
				}
			}
		}()
		c.Next()
		if c.Writer.Written() || len(c.Errors) == 0 {
			return
		}
		write(installedRenderer(rd, baseOptions, resourceRenderingFrom(c.Request.Context())), c, c.Errors.Last().Err)
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

func Routing(engine *gin.Engine, options ...crudhttp.RenderOption) {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	refuse := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) {
			if c.Writer.Written() {
				return
			}
			write(rd, c, crudhttp.Routed(status))
		}
	}
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(refuse(http.StatusNotFound))
	engine.NoMethod(refuse(http.StatusMethodNotAllowed))
}
