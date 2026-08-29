package authgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

// Routes answers what this application actually serves, for [authhttp.Verify]
// to compare against what the modules declared.
//
// It reads Gin's own routing table rather than a list kept alongside it. That is
// the entire point of the gate: a declaration is only worth checking against a
// second statement that was arrived at independently, and a recording wrapper
// around registration would agree with the declaration exactly when both were
// wrong.
//
// HEAD and OPTIONS are left out, for the same reason the other bindings leave
// them out: neither is an endpoint a module wrote. Gin does not generate a HEAD
// the way Fiber does, so on this binding one that appears was registered on
// purpose — and is skipped anyway, because the three bindings must not disagree
// about what a declaration has to cover.
func Routes(engine *gin.Engine) []authhttp.Route {
	registered := engine.Routes()
	out := make([]authhttp.Route, 0, len(registered))
	for _, route := range registered {
		if route.Method == http.MethodHead || route.Method == http.MethodOptions {
			continue
		}
		out = append(out, authhttp.Route{Method: route.Method, Path: route.Path})
	}
	return out
}

// Verify is the whole gate in one call, for the ordinary case of an application
// that mounts everything under one prefix.
//
//	if err := authgin.Verify(engine, declared, authhttp.UnderPrefix("/api/v1")); err != nil {
//		return err
//	}
//
// Returning the error rather than logging it is the difference between a
// deployment that refuses to start and one that serves an undeclared endpoint
// with a warning nobody reads.
func Verify(engine *gin.Engine, declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, Routes(engine), options...)
}
