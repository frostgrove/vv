package authfiber_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
)

// serve runs one request through the middleware and answers what the handler
// saw and what the client got.
func serve(t *testing.T, g *auth.Guard, header string) (*seen, *http.Response, string) {
	t.Helper()
	h := &seen{}
	app := fiber.New()
	app.Use(authfiber.Middleware(g))
	app.Get("/articles", h.handle)

	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return h, resp, string(body)
}

func TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal(t *testing.T) {
	h, resp, _ := serve(t, auth.NewGuard(accepts()), "Bearer t")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an authenticated request answered %d, want 200", resp.StatusCode)
	}
	if !h.ran {
		t.Fatal("the handler behind the middleware never ran")
	}
	// This is the assertion that pins SetContext rather than Locals: a
	// principal in Locals would leave found false here, and would leave every
	// policy downstream unable to see one.
	if !h.found {
		t.Fatal("the handler saw no principal on c.Context, so no policy downstream would see one either")
	}
	if h.principal.Subject() != "u-1" {
		t.Fatalf("the handler saw subject %q, want u-1", h.principal.Subject())
	}
}

func TestAnUnauthenticatedRequestIs401AndTheHandlerNeverRuns(t *testing.T) {
	t.Run("no credential at all", func(t *testing.T) {
		h, resp, _ := serve(t, auth.NewGuard(accepts()), "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a request with no credential answered %d, want 401", resp.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran for a request nobody authenticated")
		}
	})

	t.Run("a credential that does not verify", func(t *testing.T) {
		h, resp, _ := serve(t, auth.NewGuard(refuses()), "Bearer forged")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a forged credential answered %d, want 401", resp.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran for a request that failed to authenticate")
		}
	})
}

func TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason(t *testing.T) {
	_, resp, body := serve(t, auth.NewGuard(refuses()), "Bearer forged")

	if !strings.Contains(body, `"unauthenticated"`) {
		t.Fatalf("the refusal carries no code a client can branch on: %s", body)
	}
	if !strings.Contains(body, "authentication is required") {
		t.Fatalf("the refusal carries no message: %s", body)
	}
	if strings.Contains(body, badToken) {
		t.Fatalf("the refusal says which half of the token was wrong: %s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the refusal was written as %q, want JSON", ct)
	}
}

func TestAnOptionalGuardLetsAnAnonymousRequestThrough(t *testing.T) {
	t.Run("no credential reaches the handler unauthenticated", func(t *testing.T) {
		h, resp, _ := serve(t, auth.NewGuard(accepts(), auth.Optional()), "")
		if resp.StatusCode != http.StatusOK || !h.ran {
			t.Fatalf("an optional guard refused an anonymous request: %d", resp.StatusCode)
		}
		if h.found {
			t.Fatal("an optional guard invented a principal for a request that presented none")
		}
	})

	t.Run("a bad credential is still refused", func(t *testing.T) {
		h, resp, _ := serve(t, auth.NewGuard(refuses(), auth.Optional()), "Bearer forged")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("an optional guard accepted a forged token as anonymous: %d", resp.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran with a forged token downgraded to anonymous")
		}
	})
}

func TestADoubleInstallAuthenticatesOnce(t *testing.T) {
	n := 0
	g := auth.NewGuard(counting(&n))
	h := &seen{}

	app := fiber.New()
	app.Use(authfiber.Middleware(g))
	app.Use(authfiber.Middleware(g))
	app.Get("/articles", h.handle)

	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if !h.found {
		t.Fatal("a double install lost the principal")
	}
	if n != 1 {
		t.Fatalf("the credential was verified %d times; a guard mounted on the app and again on a group pays twice", n)
	}
}

func TestANilGuardRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Middleware accepted a nil guard, so nothing is authenticated and every request looks fine")
		}
	}()
	authfiber.Middleware(nil)
}
