package crudfiber

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// Every fixed path is mounted next to /:id and reached ahead of it.
//
// This binding is the one of the three where that is not free. ServeMux gives
// the more specific pattern precedence and Gin resolves a static segment ahead
// of a parameter one, so on those two the registration order in Register does
// not matter. Fiber's router matches in the order routes were registered, so
// here it is the only thing keeping GET /widgets/count from being read as an
// entity whose id is "count" — a 400 from id coercion, on a route the caller
// believed was a count.
//
// The control case is the point of the test: it shows the :id route really is
// live and really would have taken these paths, so if the static routes ever
// stop being mounted first this test fails instead of passing on an empty
// router ([[FL-013]]).
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

	// The control: a segment that is not one of the fixed paths does reach the
	// :id route, and "not-a-number" fails to coerce to the int64 key. Without
	// the fixed routes above, "count" would have arrived here the same way.
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

// Registering on a router directly is the degenerate case of Routes: the routes
// land at the root of whatever the caller mounted them on.
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

// The standalone app Routes builds carries a body cap of its own, so a body
// past it is refused by this library with this library's envelope rather than
// by Fiber with its plain-text one.
//
// Register cannot do this and does not try: the limit belongs to the app, which
// the caller owns there. That difference is the binding's, and [[FL-013]] carries
// it.
func TestTheStandaloneAppCarriesTheHandlersBodyCap(t *testing.T) {
	h := New[Widget, int64, WidgetUpdate](newFake(), MaxBody[Widget, int64, WidgetUpdate](64))

	if got := h.Routes().Config().BodyLimit; got != 65 {
		t.Fatalf("the standalone app accepts %d bytes, want one past the handler's cap of 64", got)
	}

	// The control: a handler that named no cap gets the default one, not zero.
	// Fiber reads a BodyLimit of zero as "no limit", so a default that fell
	// through as zero would take the cap off entirely on this one binding.
	plain := New[Widget, int64, WidgetUpdate](newFake())
	if got := plain.Routes().Config().BodyLimit; got <= 1 {
		t.Fatalf("an unconfigured app accepts %d bytes, which Fiber reads as no limit at all", got)
	}
}

// A verb a route does not have is 405, and this binding is one of the two that
// can say so.
//
// Fiber raises its own *fiber.Error before any handler runs, so the middleware
// has something to render. crudnet has no seam for it — a ServeMux answers the
// 405 itself, past every handler — and [[FL-013]] carries that difference rather
// than the triplet's test names disagreeing about it.
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

	// The control. The very same path with the verb it does have is served, so
	// the refusal above is about the method and not about the path.
	if r := do(t, app, http.MethodGet, "/widgets", ""); r.status != http.StatusOK {
		t.Fatalf("the path itself answered %d, so the 405 above proves nothing", r.status)
	}
}
