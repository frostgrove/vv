package authgin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/auth/http/authgin"
)

// What this binding does that the other two do not.
//
// Gin carries an error bag on the context that its own logging middleware
// reads, and there is no equivalent on net/http or Fiber — so this behaviour has
// nowhere to be mirrored to, and lives here rather than making the triplet's
// test names disagree. [[FL-019]] carries the difference.

// Gin's own logging middleware reads c.Errors, and the body deliberately does
// not carry the cause. Filing it is what keeps the reason reachable in a log.
func TestTheCauseIsFiledWithGinsErrorBag(t *testing.T) {
	var filed int
	h := &seen{}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Next(); filed = len(c.Errors) })
	r.Use(authgin.Middleware(auth.NewGuard(refuses())))
	r.GET("/articles", h.handle)

	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	req.Header.Set("Authorization", "Bearer forged")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if filed == 0 {
		t.Fatal("the refusal was not filed with c.Error, so Gin's logger sees nothing at all")
	}
}
