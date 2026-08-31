package crudfiber

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestStaticRoutesAreNotSwallowedByTheIDRoute(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		wantCalls                  []string
	}{
		{"count", http.MethodGet, "/widgets/count", "", []string{"Count"}},
		{"query", http.MethodPost, "/widgets/query", `{"limit":5}`, []string{"Get"}},
		{"bulk delete", http.MethodPost, "/widgets/bulk-delete", `{"ids":[1]}`, []string{"Delete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, fake := mount(t)

			ok(t, app, tc.method, tc.target, tc.body, http.StatusOK)

			if got := fake.methods(); !slices.Equal(got, tc.wantCalls) {
				t.Fatalf("%s %s asked the repository for %v, want %v; the :id route swallowed it",
					tc.method, tc.target, got, tc.wantCalls)
			}
		})
	}

	t.Run("control: an unclaimed segment reaches the id route", func(t *testing.T) {
		app, fake := mount(t)

		r := do(t, app, http.MethodGet, "/widgets/not-a-number", "")

		if r.status != http.StatusBadRequest {
			t.Fatalf("an unclaimed segment answered %d, want 400 from id coercion: %s", r.status, r.body)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("an id that does not parse still reached the repository: %v", fake.methods())
		}
	})
}

func TestMountingAtTheRootDoesNotCollide(t *testing.T) {
	fake := newFake()
	app := fiber.New()

	New[Widget, int64, WidgetUpdate](fake).Register(app)

	ok(t, app, http.MethodGet, "/", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/count", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/42", "", http.StatusOK)

	want := []string{"Get", "Count", "GetByID"}
	if got := fake.methods(); !slices.Equal(got, want) {
		t.Fatalf("mounted at the root the app answered with %v, want %v", got, want)
	}
}

func TestTheStandaloneAppCarriesTheHandlersBodyCap(t *testing.T) {
	h := New[Widget, int64, WidgetUpdate](newFake(), MaxBody[Widget, int64, WidgetUpdate](64))

	if got := h.Routes().Config().BodyLimit; got != 65 {
		t.Fatalf("the standalone app accepts %d bytes, want one past the handler's cap of 64", got)
	}

	plain := New[Widget, int64, WidgetUpdate](newFake())
	if got := plain.Routes().Config().BodyLimit; got <= 1 {
		t.Fatalf("an unconfigured app accepts %d bytes, which Fiber reads as no limit at all", got)
	}
}

func TestAVerbARouteDoesNotHaveIsA405(t *testing.T) {
	app := fiber.New()
	app.Use(Errors())
	app.Get("/widgets", func(c fiber.Ctx) error { return c.SendString("ok") })

	r := do(t, app, http.MethodDelete, "/widgets", "")

	if r.status != http.StatusMethodNotAllowed {
		t.Fatalf("a verb the route does not have answered %d, want 405: %s", r.status, r.body)
	}
	if !strings.Contains(string(r.body), `"method_not_allowed"`) {
		t.Fatalf("405 does not carry a code of its own, so a client cannot tell it from any other refusal: %s", r.body)
	}

	if r := do(t, app, http.MethodGet, "/widgets", ""); r.status != http.StatusOK {
		t.Fatalf("the path itself answered %d, so the 405 above proves nothing", r.status)
	}
}
