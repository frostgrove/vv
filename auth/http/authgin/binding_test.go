package authgin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authgin"
)

func TestTheCauseIsFiledWithGinsErrorBag(t *testing.T) {
	var filed int
	h := &seen{}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Next(); filed = len(c.Errors) })
	r.Use(authgin.Middleware(auth.NewGuard(refuses())))
	r.GET("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer forged")
	r.ServeHTTP(httptest.NewRecorder(), request)

	if filed == 0 {
		t.Fatal("the refusal was not filed with c.Error, so Gin's logger sees nothing at all")
	}
}
