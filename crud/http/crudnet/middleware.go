package crudnet

import (
	"net/http"

	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/port"
)

type HandlerFunc func(http.ResponseWriter, *http.Request) error

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

func WithErrors(f HandlerFunc, options ...crudhttp.RenderOption) http.Handler {
	return Errors(options...)(f)
}

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

func (this *recorder) Unwrap() http.ResponseWriter { return this.ResponseWriter }

func Routing(mux *http.ServeMux, options ...crudhttp.RenderOption) {
	rd := crudhttp.Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = crudhttp.NewRenderer(options...)
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render(rd, w, r, crudhttp.Routed(http.StatusNotFound))
	}))
}
