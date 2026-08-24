package crudgin

import (
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// Every fixed path is mounted next to /:id. Gin resolves a static segment ahead
// of a parameter one, so unlike Fiber this does not depend on the order the
// routes were registered in — but a caller cannot see the difference, and the
// guarantee is that GET /widgets/count counts rather than fetching an entity
// called "count".
//
// The control case is the point of the test: it shows the :id route really is
// live and really would have taken these paths, so if the static routes ever
// stop being mounted this test fails instead of passing on an empty router.
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

// The collection routes are registered as "" so that GET /widgets matches. The
// "/" form would mount them at /widgets/, which is a different path — the
// trailing-slash spelling is left to Gin's own redirect, which is on by
// default.
func TestTheCollectionRouteAnswersWithoutATrailingSlash(t *testing.T) {
	app, fake := mount(t)

	ok(t, app, http.MethodGet, "/widgets", "", http.StatusOK)

	if got := fake.methods(); !slices.Equal(got, []string{"Get"}) {
		t.Fatalf("GET /widgets asked the repository for %v, want [Get]", got)
	}

	t.Run("the trailing-slash spelling redirects", func(t *testing.T) {
		app, _ := mount(t)

		r := do(t, app, http.MethodGet, "/widgets/", "")

		if r.status != http.StatusMovedPermanently {
			t.Fatalf("GET /widgets/ answered %d, want a 301 to /widgets: %s", r.status, r.body)
		}
	})
}

// Mounting on the engine itself is the degenerate case of Mount: the prefix is
// "/" and the collection routes land on the root path. Registering both "" and
// "/" would collapse to the same path here and make Gin panic, which is why
// only one form is registered.
func TestMountingAtTheRootDoesNotCollide(t *testing.T) {
	fake := newFake()
	app := gin.New()

	New(fake).Register(app)

	ok(t, app, http.MethodGet, "/", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/count", "", http.StatusOK)
	ok(t, app, http.MethodGet, "/42", "", http.StatusOK)

	want := []string{"Get", "Count", "GetByID"}
	if got := fake.methods(); !slices.Equal(got, want) {
		t.Fatalf("mounted at the root the engine answered with %v, want %v", got, want)
	}
}
