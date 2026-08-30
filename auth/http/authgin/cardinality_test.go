package authgin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authgin"
)

func TestCredentialCardinalityIsSingularForEveryTransportSource(t *testing.T) {
	for _, source := range []struct {
		name    string
		header  string
		options []auth.Option
	}{
		{name: "Authorization", header: auth.HeaderAuthorization},
		{name: "configured header", header: "X-Credential", options: []auth.Option{auth.Header("X-Credential")}},
	} {
		t.Run(source.name, func(t *testing.T) {
			t.Run("one value is authenticated", func(t *testing.T) {
				h, response, calls := serveGinHeaderValues(t, source.header, []string{"Bearer one"}, source.options...)
				if response.Code != http.StatusOK || !h.ran || calls != 1 {
					t.Fatalf("a singular credential answered %d, ran=%v, authentications=%d", response.Code, h.ran, calls)
				}
			})
			t.Run("optional absence is anonymous", func(t *testing.T) {
				options := append(append([]auth.Option(nil), source.options...), auth.Optional())
				h, response, calls := serveGinHeaderValues(t, source.header, nil, options...)
				if response.Code != http.StatusOK || !h.ran || h.found || calls != 0 {
					t.Fatalf("optional absence answered %d, ran=%v, principal=%v, authentications=%d", response.Code, h.ran, h.found, calls)
				}
			})

			for _, duplicate := range []struct {
				name     string
				values   []string
				optional bool
			}{
				{name: "different duplicates", values: []string{"Bearer first", "Bearer second"}},
				{name: "identical duplicates", values: []string{"Bearer same", "Bearer same"}},
				{name: "optional still refuses duplicates", values: []string{"Bearer same", "Bearer same"}, optional: true},
			} {
				t.Run(duplicate.name, func(t *testing.T) {
					options := append([]auth.Option(nil), source.options...)
					if duplicate.optional {
						options = append(options, auth.Optional())
					}
					h, response, calls := serveGinHeaderValues(t, source.header, duplicate.values, options...)
					if response.Code != http.StatusUnauthorized || h.ran || calls != 0 {
						t.Fatalf("duplicate credentials answered %d, ran=%v, authentications=%d", response.Code, h.ran, calls)
					}
				})
			}
		})
	}
}

func serveGinHeaderValues(
	t *testing.T,
	name string,
	values []string,
	options ...auth.Option,
) (*seen, *httptest.ResponseRecorder, int) {
	t.Helper()
	calls := 0
	handler := &seen{}
	router := gin.New()
	router.Use(authgin.Middleware(auth.NewGuard(counting(&calls), options...)))
	router.GET("/articles", handler.handle)
	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header[name] = append([]string(nil), values...)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return handler, response, calls
}
