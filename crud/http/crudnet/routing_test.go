package crudnet

import (
	"net/http"
	"slices"
	"testing"
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
				t.Fatalf("%s %s asked the repository for %v, want %v; the {id} route swallowed it",
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
