package crudnet

import (
	"net/http"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

// A HandlerFunc is an ordinary handler that may return an error, which is the
// shape net/http does not have and the middleware needs.
//
// It satisfies http.Handler, so a chi, gorilla/mux or ServeMux route takes one
// directly.
type HandlerFunc func(http.ResponseWriter, *http.Request) error

// ServeHTTP renders whatever the handler returned.
//
// When the writer is the middleware's own, the error is handed up rather than
// rendered here. That is what makes a double install render once: the inner
// copy records, the outer copy writes.
func (this HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := this(w, r)
	if err == nil {
		return
	}
	if rec, ok := w.(*recorder); ok {
		rec.err = err
		return
	}
	render(defaultRenderer, w, r, err)
}

// WithErrors adapts one error-returning handler, rendering whatever it returns
// through the shared envelope.
func WithErrors(f HandlerFunc, options ...crudhttp.RenderOption) http.Handler {
	return Errors(options...)(f)
}

// Errors is the middleware. It renders an error a [HandlerFunc] returned,
// recovers a panic into a silent 500, and leaves alone a handler that already
// wrote a response.
//
// It covers this binding's own routes too — they are ordinary
// http.HandlerFuncs that write their own failures — so mounting it over a
// mux carrying both CRUD routes and hand-rolled ones is one call.
//
// Installing it twice renders once. The marker is the response-writer wrapper
// rather than anything on the error: a Fault is a value two goroutines may
// render at once, and [[D-042]] treats it as immutable.
func Errors(options ...crudhttp.RenderOption) func(http.Handler) http.Handler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, already := w.(*recorder); already {
				next.ServeHTTP(w, r)
				return
			}
			rec := &recorder{ResponseWriter: w}
			defer func() {
				// A renderer bug must not become a dropped connection. If
				// nothing has been written the client still gets the silent
				// 500; if something has, the status is already gone and there
				// is nothing to do but log.
				if p := recover(); p != nil {
					port.Logger(r.Context()).Error("crudnet: panic while serving a request",
						"method", r.Method, "path", r.URL.Path, "panic", p)
					if !rec.wrote {
						writeJSON(r.Context(), rec, http.StatusInternalServerError, crudhttp.Internal())
					}
				}
			}()
			next.ServeHTTP(rec, r)
			if rec.err != nil && !rec.wrote {
				render(rd, rec, r, rec.err)
			}
		})
	}
}

// recorder is the marker and the guard in one. Anything written through it sets
// wrote, so a handler that answered for itself is left alone — writing a second
// body produces a corrupt one.
type recorder struct {
	http.ResponseWriter
	wrote bool
	err   error
}

func (this *recorder) WriteHeader(status int) {
	this.wrote = true
	this.ResponseWriter.WriteHeader(status)
}

func (this *recorder) Write(b []byte) (int, error) {
	this.wrote = true
	return this.ResponseWriter.Write(b)
}

// Unwrap is what http.ResponseController uses to reach the real writer, so
// flushing and hijacking still work through the wrapper.
func (this *recorder) Unwrap() http.ResponseWriter { return this.ResponseWriter }
