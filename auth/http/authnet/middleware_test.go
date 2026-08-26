package authnet_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authnet"
)

// serve runs one request through the middleware and answers what the handler
// saw and what the client got.
func serve(t *testing.T, g *auth.Guard, header string) (*seen, *httptest.ResponseRecorder) {
	t.Helper()
	h := &seen{}
	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	authnet.Middleware(g)(h).ServeHTTP(w, req)
	return h, w
}

func TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal(t *testing.T) {
	h, w := serve(t, auth.NewGuard(accepts()), "Bearer t")

	if w.Code != http.StatusOK {
		t.Fatalf("an authenticated request answered %d, want 200", w.Code)
	}
	if !h.ran {
		t.Fatal("the handler behind the middleware never ran")
	}
	if !h.found {
		t.Fatal("the handler saw no principal, so no policy downstream would see one either")
	}
	if h.principal.Subject() != "u-1" {
		t.Fatalf("the handler saw subject %q, want u-1", h.principal.Subject())
	}
}

func TestAnUnauthenticatedRequestIs401AndTheHandlerNeverRuns(t *testing.T) {
	t.Run("no credential at all", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(accepts()), "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("a request with no credential answered %d, want 401", w.Code)
		}
		if h.ran {
			t.Fatal("the handler ran for a request nobody authenticated")
		}
	})

	t.Run("a credential that does not verify", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(refuses()), "Bearer forged")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("a forged credential answered %d, want 401", w.Code)
		}
		if h.ran {
			t.Fatal("the handler ran for a request that failed to authenticate")
		}
	})
}

func TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason(t *testing.T) {
	_, w := serve(t, auth.NewGuard(refuses()), "Bearer forged")
	body := w.Body.String()

	if !strings.Contains(body, `"unauthenticated"`) {
		t.Fatalf("the refusal carries no code a client can branch on: %s", body)
	}
	if !strings.Contains(body, "authentication is required") {
		t.Fatalf("the refusal carries no message: %s", body)
	}
	if strings.Contains(body, badToken) {
		t.Fatalf("the refusal says which half of the token was wrong: %s", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the refusal was written as %q, want JSON", ct)
	}
}

func TestAnOptionalGuardLetsAnAnonymousRequestThrough(t *testing.T) {
	t.Run("no credential reaches the handler unauthenticated", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(accepts(), auth.Optional()), "")
		if w.Code != http.StatusOK || !h.ran {
			t.Fatalf("an optional guard refused an anonymous request: %d", w.Code)
		}
		if h.found {
			t.Fatal("an optional guard invented a principal for a request that presented none")
		}
	})

	// The arm that makes the option safe rather than a hole.
	t.Run("a bad credential is still refused", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(refuses(), auth.Optional()), "Bearer forged")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("an optional guard accepted a forged token as anonymous: %d", w.Code)
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

	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	authnet.Middleware(g)(authnet.Middleware(g)(h)).ServeHTTP(w, req)

	if !h.found {
		t.Fatal("a double install lost the principal")
	}
	if n != 1 {
		t.Fatalf("the credential was verified %d times; a guard mounted globally and again on a subtree pays twice", n)
	}
}

func TestANilGuardRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Middleware accepted a nil guard, so nothing is authenticated and every request looks fine")
		}
	}()
	authnet.Middleware(nil)
}
