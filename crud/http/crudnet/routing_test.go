package crudnet

import (
	"net/http"
	"slices"
	"testing"
)

// Every fixed path is mounted next to /{id}. ServeMux gives the more specific
// pattern precedence, so unlike Fiber this does not depend on the order the
// routes were registered in — but a caller cannot see the difference, and the
// guarantee is that GET /widgets/count counts rather than fetching an entity
// called "count".
//
// The control case is the point of the test: it shows the {id} route really is
// live and really would have taken these paths, so if the static routes ever
// stop being mounted this test fails instead of passing on an empty mux.
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
				t.Fatalf("%s %s asked the repository for %v, want %v; the {id} route swallowed it",
					tc.method, tc.target, got, tc.wantCalls)
			}
		})
	}

	// The control: a segment that is not one of the fixed paths does reach the
	// {id} route, and "not-a-number" fails to coerce to the int64 key. Without
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

// ServeMux has no trailing-slash redirect of its own: "/widgets" and
// "/widgets/" are two patterns, and whichever is not registered answers 404. So
// Mount registers both, and both reach the same handler.
func TestBothSpellingsOfTheCollectionAnswer(t *testing.T) {
	for _, target := range []string{"/widgets", "/widgets/"} {
		t.Run(target, func(t *testing.T) {
			app, fake := mount(t)

			ok(t, app, http.MethodGet, target, "", http.StatusOK)

			if got := fake.methods(); !slices.Equal(got, []string{"Get"}) {
				t.Fatalf("GET %s asked the repository for %v, want [Get]", target, got)
			}
		})
	}

	t.Run("and so does a create", func(t *testing.T) {
		app, fake := mount(t)

		ok(t, app, http.MethodPost, "/widgets/", `{"name":"bolt"}`, http.StatusCreated)

		if got := fake.methods(); !slices.Equal(got, []string{"Save"}) {
			t.Fatalf("POST /widgets/ asked the repository for %v, want [Save]", got)
		}
	})
}

// Mounted at the root the collection is registered as "/{$}", which matches the
// root path and nothing else. A bare "/" is ServeMux's catch-all: it would match
// every path in the process that no other pattern claims, so one resource
// mounted at the root would quietly answer for every URL the application never
// registered — with a 200 and a page of widgets, which is worse than a 404
// because nothing looks wrong.
func TestMountingAtTheRootClaimsOnlyTheRootPath(t *testing.T) {
	fake := newFake()
	mux := http.NewServeMux()
	New(fake).Mount(mux, "")

	ok(t, mux, http.MethodGet, "/", "", http.StatusOK)
	ok(t, mux, http.MethodGet, "/count", "", http.StatusOK)
	ok(t, mux, http.MethodGet, "/42", "", http.StatusOK)

	want := []string{"Get", "Count", "GetByID"}
	if got := fake.methods(); !slices.Equal(got, want) {
		t.Fatalf("mounted at the root the mux answered with %v, want %v", got, want)
	}

	// The control, and the whole reason this test exists: a path nobody
	// registered is not this resource. With a catch-all "/" it would be — the
	// request would reach the list handler and answer 200.
	t.Run("an unclaimed path is not swallowed", func(t *testing.T) {
		fake := newFake()
		mux := http.NewServeMux()
		New(fake).Mount(mux, "")

		r := do(t, mux, http.MethodGet, "/nothing/here", "")

		if r.status != http.StatusNotFound {
			t.Fatalf("an unregistered path answered %d, want 404: %s", r.status, r.body)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("an unregistered path reached the repository: %v", fake.methods())
		}
	})
}

// This binding has no test for a 405, and the reason is not that the behaviour
// differs.
//
// [http.ServeMux] answers a known path with an unknown verb by itself, before
// any handler runs and past every middleware — there is no seam to render it
// through, the way crudfiber's *fiber.Error and crudgin's NoMethod are. What a
// client gets on net/http is the standard library's plain-text 405 and not this
// library's envelope. crudnet.Routing says so in its own documentation, and
// [[FL-013]] carries the difference.
