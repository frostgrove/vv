package crudgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

const installed = "crudgin.errors"

func Errors(options ...crudhttp.RenderOption) gin.HandlerFunc {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	return func(c *gin.Context) {
		if _, already := c.Get(installed); already {
			c.Next()
			return
		}
		c.Set(installed, true)
		defer func() {
			if p := recover(); p != nil {
				port.Logger(c.Request.Context()).Error("crudgin: panic while serving a request",
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
		write(rd, c, c.Errors.Last().Err)
	}
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
