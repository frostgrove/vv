package authnet

import (
	"net/http"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/internal/nilvalue"
	"github.com/frostgrove/vv/port/porthttp"
)

func Middleware(guard *auth.Guard, options ...porthttp.RenderOption) func(http.Handler) http.Handler {
	if err := guard.Validate(); err != nil {
		panic("authnet: Middleware needs a ready Guard: " + err.Error())
	}
	renderer := authhttp.RendererFor(options)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := guard.AuthenticateValues(r.Context(), r.Header.Values)
			if err != nil {
				authhttp.Refuse(w, r, renderer, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func Handler(guard *auth.Guard, next http.Handler, options ...porthttp.RenderOption) http.Handler {
	return Middleware(guard, options...)(next)
}

func SkipPreflight(middleware func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return AnswerPreflight(middleware, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(authhttp.PreflightStatus)
	}))
}

func AnswerPreflight(
	middleware func(http.Handler) http.Handler,
	preflight http.Handler,
) func(http.Handler) http.Handler {
	if nilvalue.Is(preflight) {
		panic("authnet: AnswerPreflight needs something to answer a preflight with")
	}
	return func(next http.Handler) http.Handler {
		guarded := middleware(next)
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if authhttp.Preflight(request.Method, request.Header.Get) {
				preflight.ServeHTTP(writer, request)
				return
			}
			guarded.ServeHTTP(writer, request)
		})
	}
}
