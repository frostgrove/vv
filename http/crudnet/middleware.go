package crudnet

import (
	"log"
	"net/http"

	"github.com/shardit-io/vv/http/crudhttp"
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
func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
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
func WithErrors(f HandlerFunc, opts ...crudhttp.RenderOption) http.Handler {
	return Errors(opts...)(f)
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
func Errors(opts ...crudhttp.RenderOption) func(http.Handler) http.Handler {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(opts) > 0 {
		rd = crudhttp.NewRenderer(opts...)
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
					log.Printf("crudnet: panic while serving %s %s: %v", r.Method, r.URL.Path, p)
					if !rec.wrote {
						writeJSON(rec, http.StatusInternalServerError, crudhttp.Internal())
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

func (r *recorder) WriteHeader(status int) {
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Unwrap is what http.ResponseController uses to reach the real writer, so
// flushing and hijacking still work through the wrapper.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
