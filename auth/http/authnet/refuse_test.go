package authnet_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port"
)

type twoChallenges struct{}

func (twoChallenges) Render(context.Context, error) (int, http.Header, any) {
	return http.StatusUnauthorized, http.Header{
		"Www-Authenticate": []string{`Bearer realm="api"`, `Basic realm="api"`},
		"X-Refusal":        []string{"one"},
	}, nil
}

type localeRecorder struct{ got string }

func (this *localeRecorder) Render(ctx context.Context, _ error) (int, http.Header, any) {
	this.got = port.LocaleFrom(ctx)
	return http.StatusUnauthorized, nil, nil
}

func TestARefusalCarriesEveryHeaderTheRendererAskedFor(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authhttp.Refuse(writer, request, twoChallenges{}, auth.Unauthenticated("nothing here verifies"))
	})
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/articles", nil))

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

func TestARefusalKeepsALocaleAlreadyBoundByTheApplication(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/articles", nil)
	r.Header.Set("Accept-Language", "de-DE")
	r = r.WithContext(port.WithLocale(r.Context(), "fr-CA"))
	renderer := &localeRecorder{}
	authhttp.Refuse(httptest.NewRecorder(), r, renderer, auth.Unauthenticated("nothing here verifies"))
	if renderer.got != "fr-CA" {
		t.Fatalf("the renderer was handed locale %q, want the already-bound fr-CA", renderer.got)
	}
}
