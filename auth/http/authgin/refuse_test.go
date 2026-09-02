package authgin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

type twoChallenges struct{}

func (twoChallenges) Render(context.Context, error) (int, http.Header, any) {
	return http.StatusUnauthorized, http.Header{
		"Www-Authenticate": []string{`Bearer realm="api"`, `Basic realm="api"`},
		"X-Refusal":        []string{"one"},
	}, nil
}

func TestARefusalCarriesEveryHeaderTheRendererAskedFor(t *testing.T) {
	router := gin.New()
	router.GET("/articles", func(ginContext *gin.Context) {
		authhttp.Refuse(ginContext.Writer, ginContext.Request, twoChallenges{},
			auth.Unauthenticated("nothing here verifies"))
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/articles", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("the refusal answered %d, want the renderer's 401", recorder.Code)
	}
	if got := recorder.Header().Values("Www-Authenticate"); len(got) != 2 {
		t.Fatalf("the renderer asked for two challenges and the response carries %v; "+
			"a client offered one scheme cannot choose the other", got)
	}
	if got := recorder.Header().Get("X-Refusal"); got != "one" {
		t.Fatalf("a header the renderer asked for reads as %q on the response", got)
	}
}
