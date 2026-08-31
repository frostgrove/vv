package authgin

import (
	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

func Middleware(guard *auth.Guard, options ...porthttp.RenderOption) gin.HandlerFunc {
	if err := guard.Validate(); err != nil {
		panic("authgin: Middleware needs a ready Guard: " + err.Error())
	}
	renderer := authhttp.RendererFor(options)
	return func(ginContext *gin.Context) {
		ctx, err := guard.AuthenticateValues(
			ginContext.Request.Context(),
			ginContext.Request.Header.Values,
		)
		if err != nil {
			_ = ginContext.Error(err)
			authhttp.Refuse(ginContext.Writer, ginContext.Request, renderer, err)
			ginContext.Abort()
			return
		}
		ginContext.Request = ginContext.Request.WithContext(ctx)
		ginContext.Next()
	}
}
