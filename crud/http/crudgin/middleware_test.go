package crudgin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func serve(t *testing.T, h gin.HandlerFunc, mw ...gin.HandlerFunc) response {
	t.Helper()
	e := gin.New()
	for _, m := range mw {
		e.Use(m)
	}
	e.GET("/anything", h)
	return do(t, e, http.MethodGet, "/anything", "")
}

func TestTheMiddlewareRendersAnErrorTheHandlerReturned(t *testing.T) {
	r := serve(t, func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }, Errors())

	if r.status != http.StatusNotFound {
		t.Fatalf("a filed ErrNotFound answered %d, want 404: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "not_found" {
		t.Fatalf("the envelope names the error %q, want not_found", got)
	}
}

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

	silent := serve(t, func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }, Errors())
	if silent.status != http.StatusNotFound {
		t.Fatalf("a handler that wrote nothing answered %d, want the middleware's 404: %s", silent.status, silent.body)
	}
}

func TestInstallingTheMiddlewareTwiceRendersOnce(t *testing.T) {
	h := func(c *gin.Context) { _ = c.Error(crud.ErrConflict) }

	once := serve(t, h, Errors())
	twice := serve(t, h, Errors(), Errors())

	if twice.status != once.status {
		t.Fatalf("two installs answered %d where one answered %d", twice.status, once.status)
	}

	if len(twice.body) != len(once.body) {
		t.Fatalf("two installs wrote %d bytes where one wrote %d: %s", len(twice.body), len(once.body), twice.body)
	}
	if got := failed(t, twice).Code; got != "conflict" {
		t.Fatalf("the envelope names the error %q, want conflict", got)
	}
}

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

	if r := do(t, e, http.MethodGet, "/healthy", ""); r.status != http.StatusOK || string(r.body) != `{"ok":true}` {
		t.Fatalf("a successful route answered %d %s", r.status, r.body)
	}
}

func TestWithErrorHandlerStillWinsInsideErrorsMiddleware(t *testing.T) {
	fake := newFake()
	fake.err = crud.ErrNotFound
	app := gin.New()
	app.Use(Errors())
	New[Widget, int64, WidgetUpdate](fake, WithErrorHandler[Widget, int64, WidgetUpdate](
		func(c *gin.Context, _ error) {
			c.JSON(http.StatusGone, gin.H{"custom": true})
		},
	)).Mount(app, "/widgets")

	r := do(t, app, http.MethodGet, "/widgets/42", "")

	if r.status != http.StatusGone || string(r.body) != `{"custom":true}` {
		t.Fatalf("the explicit resource handler lost to the middleware: %d %s", r.status, r.body)
	}
}

type panicky struct{}

func (panicky) Message(context.Context, errs.Violation, string) (string, bool) {
	panic("the catalogue is not loaded")
}

type localeEcho struct{}

func (localeEcho) Message(_ context.Context, _ errs.Violation, locale string) (string, bool) {
	return "locale:" + locale, true
}

func TestHTTPRenderingOnlyDerivesLocaleWhenNoneIsBound(t *testing.T) {
	fault := errs.Validation().Field("name").Code(errs.CodeRequired).Fault()
	message := func(t *testing.T, bound, header string) string {
		t.Helper()
		e := gin.New()
		e.Use(func(c *gin.Context) {
			if bound != "" {
				c.Request = c.Request.WithContext(port.WithLocale(c.Request.Context(), bound))
			}
			c.Next()
		})
		e.Use(Errors(crudhttp.WithMessages(localeEcho{})))
		e.GET("/anything", func(c *gin.Context) { _ = c.Error(fault) })
		request := httptest.NewRequest(http.MethodGet, "/anything", nil)
		request.Header.Set("Accept-Language", header)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, request)
		return failed(t, response{status: w.Code, body: w.Body.Bytes(), header: w.Header()}).Message
	}

	for _, tc := range []struct {
		name, bound, header, want string
	}{
		{"a prebound locale wins", "fr", "ja", "locale:fr"},
		{"the header fills an empty context", "", "ja-JP,ja;q=0.9", "locale:ja-JP"},
		{"an empty header does not erase a locale", "fr", "", "locale:fr"},
		{"the control has no locale", "", "", "locale:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := message(t, tc.bound, tc.header); got != tc.want {
				t.Fatalf("bound %q and header %q produced %q, want %q", tc.bound, tc.header, got, tc.want)
			}
		})
	}
}

func TestAPanicInTheRendererBecomesASilent500(t *testing.T) {
	h := func(c *gin.Context) { _ = c.Error(crud.ErrNotFound) }

	r := serve(t, h, Errors(crudhttp.WithMessages(panicky{})))

	if r.status != http.StatusInternalServerError {
		t.Fatalf("a panicking renderer answered %d, want 500: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
		t.Fatalf("the recovered 500 answered %s, want nothing but the status", got)
	}

	if fine := serve(t, h, Errors()); fine.status != http.StatusNotFound {
		t.Fatalf("a working renderer answered %d, want 404: %s", fine.status, fine.body)
	}
}
