package crudnet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/http/crudhttp"
)

// serve runs one request through h and reports what the client got.
func serve(t *testing.T, h http.Handler, method, target string) response {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return response{status: w.Code, body: w.Body.Bytes(), header: w.Header()}
}

// The middleware exists so an author's own handlers answer failures the way the
// CRUD routes do, without repeating the table.
func TestTheMiddlewareRendersAnErrorTheHandlerReturned(t *testing.T) {
	h := WithErrors(func(http.ResponseWriter, *http.Request) error {
		return crud.ErrNotFound
	})

	r := serve(t, h, http.MethodGet, "/anything")

	if r.status != http.StatusNotFound {
		t.Fatalf("a returned ErrNotFound answered %d, want 404: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "not_found" {
		t.Fatalf("the envelope names the error %q, want not_found", got)
	}
}

// Writing a second body produces a corrupt one, so a handler that answered for
// itself is left alone whatever it then returns.
func TestAHandlerThatAlreadyWroteIsLeftAlone(t *testing.T) {
	wrote := WithErrors(func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"mine":true}`))
		return crud.ErrNotFound
	})

	r := serve(t, wrote, http.MethodGet, "/anything")

	if r.status != http.StatusTeapot {
		t.Fatalf("a handler that had already answered was overwritten with %d: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"mine":true}` {
		t.Fatalf("the handler's own body became %s", got)
	}

	// The control. Without it this passes for a middleware that renders
	// nothing at all, and the test above would be measuring an empty feature.
	silent := WithErrors(func(http.ResponseWriter, *http.Request) error {
		return crud.ErrNotFound
	})
	if r := serve(t, silent, http.MethodGet, "/anything"); r.status != http.StatusNotFound {
		t.Fatalf("a handler that wrote nothing answered %d, want the middleware's 404: %s", r.status, r.body)
	}
}

// A response rendered twice is the most likely way to get this wrong, and a
// double install is how it happens: once on the router, once on a group.
func TestInstallingTheMiddlewareTwiceRendersOnce(t *testing.T) {
	fn := HandlerFunc(func(http.ResponseWriter, *http.Request) error { return crud.ErrConflict })

	once := serve(t, Errors()(fn), http.MethodGet, "/anything")
	twice := serve(t, Errors()(Errors()(fn)), http.MethodGet, "/anything")

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
// reason it is a func(http.Handler) http.Handler rather than an option on the
// handler: a chi or gorilla/mux router takes it unchanged.
func TestTheMiddlewareCoversAHandRolledRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /reports", HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		return crud.ErrForbidden
	}))
	mux.Handle("GET /healthy", HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return nil
	}))
	h := Errors()(mux)

	r := serve(t, h, http.MethodGet, "/reports")
	if r.status != http.StatusForbidden {
		t.Fatalf("a hand-rolled route answered %d, want 403: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "forbidden" {
		t.Fatalf("the envelope names the error %q, want forbidden", got)
	}

	// The control: a route that succeeds is untouched. A middleware that
	// rendered an envelope over every response would pass the leg above.
	if r := serve(t, h, http.MethodGet, "/healthy"); r.status != http.StatusOK || string(r.body) != `{"ok":true}` {
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
	h := Errors(crudhttp.WithMessages(panicky{}))(HandlerFunc(
		func(http.ResponseWriter, *http.Request) error { return crud.ErrNotFound },
	))

	r := serve(t, h, http.MethodGet, "/anything")

	if r.status != http.StatusInternalServerError {
		t.Fatalf("a panicking renderer answered %d, want 500: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
		t.Fatalf("the recovered 500 answered %s, want nothing but the status", got)
	}

	// The control: the same handler with a renderer that works answers 404. A
	// middleware that always 500'd would pass the leg above.
	fine := Errors()(HandlerFunc(func(http.ResponseWriter, *http.Request) error { return crud.ErrNotFound }))
	if r := serve(t, fine, http.MethodGet, "/anything"); r.status != http.StatusNotFound {
		t.Fatalf("a working renderer answered %d, want 404: %s", r.status, r.body)
	}
}
