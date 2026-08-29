package crudgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

// installed marks a context the middleware is already running over. Gin has no
// response-writer wrapper of ours to hang the marker on — c.Writer is the
// framework's — so the context's own key store is where it goes. It is still
// per-request state and never anything on the Fault, which is a value two
// goroutines may render at once ([[D-042]]).
const installed = "crudgin.errors"

// Errors renders whatever a handler put in c.Errors, for handlers this library
// did not write.
//
// Gin handlers return nothing, so there is no error to catch — the error bag is
// the seam the framework gives, and c.Error(err) is how a handler files one.
// This binding's own routes call it too, through DefaultErrorHandler.
//
// It checks c.Writer.Written() first: a handler that already wrote a response
// is left alone, because writing a second body produces a corrupt one. And it
// is safe to install twice — once on the engine and once on a group is the
// ordinary way that happens.
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
			// A renderer bug must not become a dropped connection. If nothing
			// has been written the client still gets the silent 500; if
			// something has, the status is already gone and there is nothing to
			// do but log.
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

// Routing renders Gin's own refusals in the same envelope as everything else.
//
// A path nothing claimed and a verb a route does not have are answered by the
// router, before any handler or middleware of this library runs, so neither
// reaches the error contract on its own: Gin writes a bare 404 with no body, and
// a client that parses one shape for every failure gets nothing to parse. Worse,
// an application that maps its own unknown errors to 500 turns "you asked for
// something that is not there" into "this service is broken", which a client
// retries.
//
// HandleMethodNotAllowed is turned on here, because without it Gin answers a
// known path with an unknown verb as 404 — and the two are different statements
// to a client.
//
// Call it once, on the engine, after the routes are mounted.
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
