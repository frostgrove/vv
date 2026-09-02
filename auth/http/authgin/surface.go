package authgin

import (
	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

func Routes(engine *gin.Engine) []authhttp.Route {
	registered := engine.Routes()
	out := make([]authhttp.Route, 0, len(registered))
	for _, route := range registered {
		out = append(out, authhttp.Route{Method: route.Method, Path: route.Path})
	}
	return out
}

func Verify(engine *gin.Engine, declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, Routes(engine), options...)
}

func VerifyAreas(engine *gin.Engine, areas ...authhttp.Area) error {
	return authhttp.VerifyAreas(Routes(engine), areas...)
}
