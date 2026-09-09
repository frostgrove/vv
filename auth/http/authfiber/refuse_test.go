package authfiber

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
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
	app := fiber.New()
	app.Get("/articles", func(c fiber.Ctx) error {
		return refuse(c, twoChallenges{}, auth.Unauthenticated("nothing here verifies"))
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/articles", nil))
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the refusal answered %d, want the renderer's 401", response.StatusCode)
	}
	if got := response.Header.Values("Www-Authenticate"); len(got) != 2 {
		t.Fatalf("the renderer asked for two challenges and the response carries %v; "+
			"a client offered one scheme cannot choose the other", got)
	}
	if got := response.Header.Get("X-Refusal"); got != "one" {
		t.Fatalf("a header the renderer asked for reads as %q on the response", got)
	}
}

func TestARefusalKeepsALocaleAlreadyBoundByTheApplication(t *testing.T) {
	renderer := &localeRecorder{}
	app := fiber.New()
	app.Get("/articles", func(c fiber.Ctx) error {
		c.SetContext(port.WithLocale(c.Context(), "fr-CA"))
		return refuse(c, renderer, auth.Unauthenticated("nothing here verifies"))
	})
	r := httptest.NewRequest(http.MethodGet, "/articles", nil)
	r.Header.Set("Accept-Language", "de-DE")
	response, err := app.Test(r)
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if renderer.got != "fr-CA" {
		t.Fatalf("the renderer was handed locale %q, want the already-bound fr-CA", renderer.got)
	}
}
