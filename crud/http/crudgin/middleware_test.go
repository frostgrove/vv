package crudgin

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/errs"
)

// serve mounts one handler behind the middlewares and reports what the client
// got. Gin handlers return nothing, so a handler files its failure with
// c.Error — the error bag is the seam this framework gives.
func serve(t *testing.T, h gin.HandlerFunc, mw ...gin.HandlerFunc) response {
	t.Helper()
	e := gin.New()
	for _, m := range mw {
		e.Use(m)
	}
	e.GET("/anything", h)
	return do(t, e, http.MethodGet, "/anything", "")
}

// The middleware exists so an author's own handlers answer failures the way the
// CRUD routes do, without repeating the table.
func TestTheMiddlewareRendersAnErrorTheHandlerReturned(t *testing.T) {
	r := serve(t, func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }, Errors())

	if r.status != http.StatusNotFound {
		t.Fatalf("a filed ErrNotFound answered %d, want 404: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "not_found" {
		t.Fatalf("the envelope names the error %q, want not_found", got)
	}
}

// Writing a second body produces a corrupt one, so a handler that answered for
// itself is left alone whatever it then files.
func TestAHandlerThatAlreadyWroteIsLeftAlone(t *testing.T) {
	r := serve(t, func(c *gin.Context) {
		c.JSON(http.StatusTeapot, gin.H{"mine": true})
		_ = c.Error(crud.ErrNotFound)
	}, Errors())

	if r.status != http.StatusTeapot {
		t.Fatalf("a handler that had already answered was overwritten with %d: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"mine":true}` {
		t.Fatalf("the handler's own body became %s", got)
	}

	// The control. Without it this passes for a middleware that renders
	// nothing at all, and the test above would be measuring an empty feature.
	silent := serve(t, func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }, Errors())
	if silent.status != http.StatusNotFound {
		t.Fatalf("a handler that wrote nothing answered %d, want the middleware's 404: %s", silent.status, silent.body)
	}
}

// A response rendered twice is the most likely way to get this wrong, and a
// double install is how it happens: once on the engine, once on a group.
func TestInstallingTheMiddlewareTwiceRendersOnce(t *testing.T) {
	h := func(c *gin.Context) { _ = c.Error(crud.ErrConflict) }

	once := serve(t, h, Errors())
	twice := serve(t, h, Errors(), Errors())

	if twice.status != once.status {
		t.Fatalf("two installs answered %d where one answered %d", twice.status, once.status)
	}
	// The byte length, not only "it decodes": two envelopes concatenated
	// decode as the first one and every field assertion would pass.
	if len(twice.body) != len(once.body) {
		t.Fatalf("two installs wrote %d bytes where one wrote %d: %s", len(twice.body), len(once.body), twice.body)
	}
	if got := failed(t, twice).Code; got != "conflict" {
		t.Fatalf("the envelope names the error %q, want conflict", got)
	}
}

// The same middleware covers a route this library never wrote, which is the
// reason it is a plain gin.HandlerFunc: an engine carrying both CRUD routes and
// hand-rolled ones installs it once.
func TestTheMiddlewareCoversAHandRolledRoute(t *testing.T) {
	e := gin.New()
	e.Use(Errors())
	e.GET("/reports", func(c *gin.Context) { _ = c.Error(crud.ErrForbidden) })
	e.GET("/healthy", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	r := do(t, e, http.MethodGet, "/reports", "")
	if r.status != http.StatusForbidden {
		t.Fatalf("a hand-rolled route answered %d, want 403: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "forbidden" {
		t.Fatalf("the envelope names the error %q, want forbidden", got)
	}

	// The control: a route that succeeds is untouched. A middleware that
	// rendered an envelope over every response would pass the leg above.
	if r := do(t, e, http.MethodGet, "/healthy", ""); r.status != http.StatusOK || string(r.body) != `{"ok":true}` {
		t.Fatalf("a successful route answered %d %s", r.status, r.body)
	}
}

// panicky is a message source that fails the way a consumer's own catalogue
// would: in the middle of rendering, after the status is decided.
type panicky struct{}

func (panicky) Message(context.Context, errs.Violation, string) (string, bool) {
	panic("the catalogue is not loaded")
}

// A renderer bug must not become a dropped connection. The client gets the same
// silent 500 any other server fault produces.
func TestAPanicInTheRendererBecomesASilent500(t *testing.T) {
	h := func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }

	r := serve(t, h, Errors(crudhttp.WithMessages(panicky{})))

	if r.status != http.StatusInternalServerError {
		t.Fatalf("a panicking renderer answered %d, want 500: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
		t.Fatalf("the recovered 500 answered %s, want nothing but the status", got)
	}

	// The control: the same handler with a renderer that works answers 404. A
	// middleware that always 500'd would pass the leg above.
	if fine := serve(t, h, Errors()); fine.status != http.StatusNotFound {
		t.Fatalf("a working renderer answered %d, want 404: %s", fine.status, fine.body)
	}
}
